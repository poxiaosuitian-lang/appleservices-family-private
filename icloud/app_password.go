package icloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
)

const appSpecificPasswordPath = "/account/manage/security/secondary-password"

var (
	ErrAppSpecificPasswordVerificationRequired = errors.New("Apple app-specific password requires additional verification")
	ErrAppSpecificPasswordCreationUncertain    = errors.New("Apple app-specific password creation outcome is uncertain")
)

// AppSpecificPassword contains metadata and, only after creation, the one-time
// secret. Password is deliberately excluded from JSON, including list output.
type AppSpecificPassword struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Password string `json:"-"`
}

// AppSpecificPasswordError never contains request/response bodies or secrets.
// CreationUncertain means a creation may have succeeded: callers must reconcile
// it rather than automatically issue another creation request.
type AppSpecificPasswordError struct {
	Code              string
	HTTPStatus        int
	CreationUncertain bool
}

func (e *AppSpecificPasswordError) Error() string {
	if e.CreationUncertain {
		return ErrAppSpecificPasswordCreationUncertain.Error()
	}
	switch e.Code {
	case "authentication-required":
		return ErrAccountManagementAuthenticationRequired.Error()
	case "verification-required":
		return ErrAppSpecificPasswordVerificationRequired.Error()
	case "password-rejected":
		return "Apple Account password confirmation was rejected"
	case "invalid-label":
		return "Apple app-specific password label is required"
	case "invalid-response":
		return "Apple app-specific password response was invalid"
	case "session-save-failed":
		return "Apple Account management session could not be saved"
	case "cancelled":
		return "Apple app-specific password request was cancelled"
	default:
		return "Apple app-specific password request failed"
	}
}

func (e *AppSpecificPasswordError) Is(target error) bool {
	return target == ErrAppSpecificPasswordCreationUncertain && e.CreationUncertain ||
		target == ErrAppSpecificPasswordVerificationRequired && e.Code == "verification-required" ||
		target == ErrAccountManagementAuthenticationRequired && e.Code == "authentication-required"
}

// ListAppSpecificPasswords returns metadata only. Apple does not provide a way
// to retrieve a previously generated secret.
func (c *AppleAccountClient) ListAppSpecificPasswords(ctx context.Context) ([]AppSpecificPassword, error) {
	if err := c.appSpecificPasswordReady(ctx); err != nil {
		return nil, err
	}
	data, status, err := c.portalRequest(ctx, http.MethodGet, appSpecificPasswordPath, nil, true)
	defer clear(data)
	if err != nil || status != http.StatusOK {
		return nil, appSpecificPasswordRequestError(status, false)
	}
	var wire []struct {
		ID          json.RawMessage `json:"id"`
		Description string          `json:"description"`
	}
	if json.Unmarshal(data, &wire) != nil || wire == nil {
		// A read-only live portal check on 2026-11-14 returned HTTP 200 with
		// {} for no app passwords. Accept only that empty-object variant;
		// null and objects with unknown fields remain invalid responses.
		var emptyObject map[string]json.RawMessage
		if json.Unmarshal(data, &emptyObject) == nil && emptyObject != nil && len(emptyObject) == 0 {
			return []AppSpecificPassword{}, nil
		}
		return nil, &AppSpecificPasswordError{Code: "invalid-response", HTTPStatus: status}
	}
	result := make([]AppSpecificPassword, 0, len(wire))
	for _, item := range wire {
		id := appSpecificPasswordID(item.ID)
		if id == "" || strings.TrimSpace(item.Description) == "" {
			return nil, &AppSpecificPasswordError{Code: "invalid-response", HTTPStatus: status}
		}
		result = append(result, AppSpecificPassword{ID: id, Label: item.Description})
	}
	return result, nil
}

// CreateAppSpecificPassword returns the secret once and does not persist it.
// Persist a creation checkpoint before calling this method. The only replay is
// after a definite 451 password challenge and successful password confirmation;
// transport failures, malformed successes and extra 2FA are never replayed.
//
// Routes, payloads and challenge handling match Apple's public account portal:
// https://appleid.cdn-apple.com/static/module-assets/home-DknVosT8.js
// (secondaryPasswords/get, secondaryPassword/update and passwordChallenge).
func (c *AppleAccountClient) CreateAppSpecificPassword(ctx context.Context, label, password string) (*AppSpecificPassword, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return nil, &AppSpecificPasswordError{Code: "invalid-label"}
	}
	if err := c.appSpecificPasswordReady(ctx); err != nil {
		return nil, err
	}
	payload := map[string]string{"description": label}
	data, status, err := c.portalRequest(ctx, http.MethodPost, appSpecificPasswordPath, payload, true)
	if status == http.StatusUnavailableForLegalReasons {
		var challenge map[string]json.RawMessage
		validChallenge := json.Unmarshal(data, &challenge) == nil && challenge != nil
		_, needs2FA := challenge["onDemand2FAToken"]
		clear(data)
		if !validChallenge || needs2FA || password == "" {
			return nil, &AppSpecificPasswordError{Code: "verification-required", HTTPStatus: status}
		}
		// The portal's 451 interceptor shows a password prompt unless the
		// response contains onDemand2FAToken. Never treat that token as a
		// normal password challenge or expose it through an error.
		authData, authStatus, authErr := c.portalRequest(ctx, http.MethodPost, "/authenticate/password", map[string]string{"password": password}, true)
		clear(authData)
		if authErr != nil {
			if authStatus == http.StatusConflict {
				return nil, &AppSpecificPasswordError{Code: "password-rejected", HTTPStatus: authStatus}
			}
			return nil, appSpecificPasswordRequestError(authStatus, false)
		}
		if saveErr := c.saveSession(); saveErr != nil {
			return nil, &AppSpecificPasswordError{Code: "session-save-failed"}
		}
		if err := c.appSpecificPasswordReady(ctx); err != nil {
			return nil, err
		}
		data, status, err = c.portalRequest(ctx, http.MethodPost, appSpecificPasswordPath, payload, true)
	}
	defer clear(data)
	if err != nil || status != http.StatusOK {
		return nil, appSpecificPasswordRequestError(status, true)
	}
	var wire struct {
		ID          json.RawMessage `json:"id"`
		Description string          `json:"description"`
		Password    string          `json:"password"`
	}
	if json.Unmarshal(data, &wire) != nil || appSpecificPasswordID(wire.ID) == "" || wire.Description != label || strings.TrimSpace(wire.Password) == "" {
		return nil, &AppSpecificPasswordError{Code: "invalid-response", HTTPStatus: status, CreationUncertain: true}
	}
	return &AppSpecificPassword{ID: appSpecificPasswordID(wire.ID), Label: wire.Description, Password: wire.Password}, nil
}

func (c *AppleAccountClient) appSpecificPasswordReady(ctx context.Context) error {
	if ctx.Err() != nil {
		return &AppSpecificPasswordError{Code: "cancelled"}
	}
	if c.apiBase == "" || c.apiKey == "" {
		return &AppSpecificPasswordError{Code: "authentication-required"}
	}
	return nil
}

func appSpecificPasswordRequestError(status int, creating bool) *AppSpecificPasswordError {
	result := &AppSpecificPasswordError{Code: "request-failed", HTTPStatus: status}
	switch status {
	case http.StatusUnauthorized, 419: // Apple's SessionTimeout status.
		result.Code = "authentication-required"
	case 451, 452, 456, 465, 466, 467, 468:
		// Apple verification, native takeover, critical edit and activation
		// challenges must be completed by the account holder.
		result.Code = "verification-required"
	default:
		result.CreationUncertain = creating && (status < 400 || status == http.StatusRequestTimeout || status >= 500)
	}
	return result
}

func appSpecificPasswordID(data json.RawMessage) string {
	data = bytes.TrimSpace(data)
	var value string
	if json.Unmarshal(data, &value) == nil {
		return strings.TrimSpace(value)
	}
	// Preserve numeric IDs exactly; decoding through float64 can lose digits.
	if number, err := strconv.ParseUint(string(data), 10, 64); err == nil && number != 0 {
		return string(data)
	}
	return ""
}

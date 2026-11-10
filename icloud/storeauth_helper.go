package icloud

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/Laky-64/appleservices/internal/backgroundexec"
	"github.com/Laky-64/appleservices/internal/sap/cachepath"
	"golang.org/x/net/publicsuffix"
)

const frameworkStoreAgent = "Configurator/2.21 (Macintosh; OS X 27.0.1; 26A434) AppleWebKit/0625.2.5.11.6"
const maxHelperMessage = 1024 * 1024

type storeAuthMessage struct {
	Version      int                `json:"version"`
	Type         string             `json:"type"`
	Email        string             `json:"email,omitempty"`
	Password     string             `json:"password,omitempty"`
	MACAddress   string             `json:"mac_address,omitempty"`
	ExpectedDSID string             `json:"expected_dsid,omitempty"`
	State        []byte             `json:"state,omitempty"`
	Code         string             `json:"code,omitempty"`
	OK           bool               `json:"ok,omitempty"`
	DSID         string             `json:"dsid,omitempty"`
	Token        string             `json:"token,omitempty"`
	Storefront   string             `json:"storefront,omitempty"`
	Pod          string             `json:"pod,omitempty"`
	GUID         string             `json:"guid,omitempty"`
	Cookies      []storeAuthCookies `json:"cookies,omitempty"`
	Stage        string             `json:"stage,omitempty"`
	HTTPStatus   int                `json:"http_status,omitempty"`
}

type storeAuthCookies struct {
	URL     string         `json:"url"`
	Cookies []*http.Cookie `json:"cookies"`
}

// CanReuseFrameworkState does not imply a valid commerce login. The opaque
// machine state is retained only after a successful, same-account authentication.
func (s *StoreCommerceSession) CanReuseFrameworkState(appleID, expectedDSID string) bool {
	return s != nil && len(s.frameworkState) > 0 && expectedDSID != "" &&
		s.frameworkDSID == expectedDSID && strings.EqualFold(s.frameworkEmail, appleID)
}

// AuthenticateFrameworkStore runs the pinned native framework implementation in
// a bounded child process. Only stdin/stdout carry credentials, never arguments,
// environment, logs or a persistent keychain. No purchase endpoint is called.
func AuthenticateFrameworkStore(ctx context.Context, appleID, password, expectedDSID string, previous *StoreCommerceSession, codeProvider func() (string, error)) (*StoreCommerceSession, error) {
	guid, hardware, err := storeHardwareIdentity()
	if err != nil || isVirtualInterface("", hardware) {
		return nil, &StorageError{Stage: "purchase-store-physical-hardware"}
	}
	path := os.Getenv("APPLESERVICES_STORE_AUTH_HELPER")
	if path == "" {
		executable, err := os.Executable()
		if err != nil {
			return nil, &StorageError{Stage: "purchase-store-runtime"}
		}
		name := "storeauth"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		path = filepath.Join(filepath.Dir(executable), name)
	}
	if !filepath.IsAbs(path) {
		return nil, &StorageError{Stage: "purchase-store-runtime"}
	}
	cache, err := cachepath.Root()
	if err != nil {
		return nil, &StorageError{Stage: "purchase-store-runtime"}
	}
	ctx, cancel := context.WithTimeout(ctx, 150*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, path)
	command.WaitDelay = 2 * time.Second
	backgroundexec.Configure(command)
	command.Env = storeAuthEnvironment(os.Environ(), cache)
	request := storeAuthMessage{Version: 1, Type: "login", Email: appleID, Password: password, ExpectedDSID: expectedDSID, MACAddress: net.HardwareAddr(hardware).String()}
	if previous.CanReuseFrameworkState(appleID, expectedDSID) && previous.frameworkGUID == guid {
		request.State = previous.frameworkState
	}
	result, err := exchangeStoreAuth(ctx, command, request, codeProvider)
	if err != nil {
		return nil, err
	}
	return importFrameworkSession(result, appleID, expectedDSID, guid)
}

func storeAuthEnvironment(original []string, cache string) []string {
	env := make([]string, 0, len(original)+2)
	for _, item := range original {
		key, _, _ := strings.Cut(item, "=")
		switch strings.ToUpper(key) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "LOCALAPPDATA", "XDG_CACHE_HOME", "APPLESERVICES_APPLEID", "APPLESERVICES_PASSWORD":
			continue
		}
		env = append(env, item)
	}
	return append(env, "LOCALAPPDATA="+cache, "XDG_CACHE_HOME="+cache)
}

func exchangeStoreAuth(ctx context.Context, command *exec.Cmd, request storeAuthMessage, provider func() (string, error)) (*storeAuthMessage, error) {
	failure := func(stage string) error {
		if ctx.Err() != nil {
			return &StorageError{Stage: "purchase-store-timeout"}
		}
		return &StorageError{Stage: stage}
	}
	in, err := command.StdinPipe()
	if err != nil {
		return nil, failure("purchase-store-runtime")
	}
	defer in.Close()
	out, err := command.StdoutPipe()
	if err != nil {
		return nil, failure("purchase-store-runtime")
	}
	defer out.Close()
	command.Stderr = io.Discard
	if err := command.Start(); err != nil {
		return nil, failure("purchase-store-runtime")
	}
	waited := false
	defer func() {
		if !waited {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	encoder := json.NewEncoder(in)
	if err := encoder.Encode(request); err != nil {
		return nil, failure("purchase-store-helper-protocol")
	}
	request.Password, request.State = "", nil
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 4096), maxHelperMessage)
	challenged := false
	for scanner.Scan() {
		var response storeAuthMessage
		if json.Unmarshal(scanner.Bytes(), &response) != nil || response.Version != 1 {
			return nil, failure("purchase-store-helper-protocol")
		}
		switch response.Type {
		case "challenge":
			if challenged || provider == nil {
				return nil, failure("purchase-store-code-required")
			}
			challenged = true
			type codeReply struct {
				code string
				err  error
			}
			provided := make(chan codeReply, 1)
			go func() { code, err := provider(); provided <- codeReply{code, err} }()
			var reply codeReply
			select {
			case <-ctx.Done():
				return nil, failure("purchase-store-timeout")
			case reply = <-provided:
			}
			if reply.err != nil || !regexp.MustCompile(`^\d{6}$`).MatchString(reply.code) {
				return nil, failure("purchase-store-code-required")
			}
			if ctx.Err() != nil {
				return nil, failure("purchase-store-timeout")
			}
			if encoder.Encode(storeAuthMessage{Version: 1, Type: "code", Code: reply.code}) != nil {
				return nil, failure("purchase-store-helper-protocol")
			}
		case "result":
			_ = in.Close()
			// The helper must exit after its result; an unexpected trailing message
			// invalidates the exchange. CommandContext still bounds this wait.
			if scanner.Scan() || scanner.Err() != nil {
				return nil, failure("purchase-store-helper-protocol")
			}
			err := command.Wait()
			waited = true
			if ctx.Err() != nil {
				return nil, failure("purchase-store-timeout")
			}
			if !response.OK {
				stage := "purchase-store-authentication"
				switch response.Stage {
				case "storeauth-code-required", "storeauth-code-invalid":
					stage = "purchase-store-code-required"
				case "storeauth-account-mismatch":
					stage = "purchase-store-account-binding"
				case "storeauth-protocol", "storeauth-input", "storeauth-cookies", "storeauth-state":
					stage = "purchase-store-helper-protocol"
				}
				status := response.HTTPStatus
				if status < 100 || status > 599 {
					status = 0
				}
				return nil, &StorageError{Stage: stage, HTTPStatus: status}
			}
			if err != nil {
				return nil, failure("purchase-store-helper-protocol")
			}
			return &response, nil
		default:
			return nil, failure("purchase-store-helper-protocol")
		}
	}
	return nil, failure("purchase-store-helper-protocol")
}

func importFrameworkSession(result *storeAuthMessage, email, expectedDSID, guid string) (*StoreCommerceSession, error) {
	bad := &StorageError{Stage: "purchase-store-account-binding"}
	if result == nil || !result.OK || expectedDSID == "" || result.DSID != expectedDSID ||
		!regexp.MustCompile(`^\d+$`).MatchString(result.DSID) || !strings.EqualFold(result.Email, email) || result.GUID != guid ||
		result.Token == "" || len(result.Token) > 65536 || strings.ContainsAny(result.Token, "\r\n") ||
		!regexp.MustCompile(`^\d{6}(?:-\d+)?(?:,\d+)*$`).MatchString(result.Storefront) ||
		(result.Pod != "" && !regexp.MustCompile(`^\d{1,5}$`).MatchString(result.Pod)) || len(result.State) > maxHelperMessage/2 {
		return nil, bad
	}
	jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
	if len(result.Cookies) > 512 {
		return nil, &StorageError{Stage: "purchase-store-helper-cookies"}
	}
	count := 0
	for _, event := range result.Cookies {
		u, err := url.Parse(event.URL)
		if err != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || u.Port() != "" ||
			!(strings.HasSuffix(u.Hostname(), ".itunes.apple.com") || u.Hostname() == "gsa.apple.com" || u.Hostname() == "gsa-ssl.apple.com" || u.Hostname() == "gsas.apple.com") {
			return nil, &StorageError{Stage: "purchase-store-helper-cookies"}
		}
		count += len(event.Cookies)
		if count > 2048 {
			return nil, &StorageError{Stage: "purchase-store-helper-cookies"}
		}
		for _, cookie := range event.Cookies {
			if cookie == nil || cookie.Valid() != nil {
				return nil, &StorageError{Stage: "purchase-store-helper-cookies"}
			}
		}
		jar.SetCookies(u, event.Cookies)
	}
	// Explicit allowlist: never retain a password, OTP, pending redemption or
	// arbitrary helper fields in the restart checkpoint.
	checkpoint, err := json.Marshal(storeAuthMessage{Version: 1, Type: "result", OK: true,
		Email: result.Email, DSID: result.DSID, Token: result.Token, Storefront: result.Storefront,
		Pod: result.Pod, GUID: result.GUID, Cookies: result.Cookies, State: result.State})
	if err != nil {
		return nil, bad
	}
	return &StoreCommerceSession{
		identity:          &storeSession{DSID: result.DSID, PasswordToken: result.Token, StoreFront: result.Storefront, Pod: result.Pod, GUID: guid},
		client:            &http.Client{Jar: jar, Transport: skillMatchedHTTPTransport(), Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		commerceUserAgent: frameworkStoreAgent,
		frameworkState:    append([]byte(nil), result.State...), frameworkDSID: result.DSID, frameworkEmail: result.Email, frameworkGUID: guid,
		frameworkCheckpoint: checkpoint,
	}, nil
}

// PrivateCheckpoint contains authentication tokens. Callers must encrypt it
// with the operating system; it is never a UI/API response or a log value.
func (s *StoreCommerceSession) PrivateCheckpoint(appleID, dsid string) ([]byte, error) {
	if !s.MatchesAccount(dsid) || !strings.EqualFold(s.frameworkEmail, appleID) || len(s.frameworkCheckpoint) == 0 {
		return nil, &StorageError{Stage: "purchase-store-checkpoint-binding"}
	}
	return append([]byte(nil), s.frameworkCheckpoint...), nil
}

// RestoreFrameworkStore binds saved tokens to both the account and physical
// Store identity. Callers must perform a read-only server check before use.
func RestoreFrameworkStore(data []byte, appleID, dsid string) (*StoreCommerceSession, error) {
	guid, _, err := storeHardwareIdentity()
	if err != nil {
		return nil, &StorageError{Stage: "purchase-store-physical-hardware"}
	}
	var result storeAuthMessage
	if len(data) == 0 || len(data) > maxHelperMessage || json.Unmarshal(data, &result) != nil || result.Password != "" || result.Code != "" {
		return nil, &StorageError{Stage: "purchase-store-checkpoint-invalid"}
	}
	return importFrameworkSession(&result, appleID, dsid, guid)
}

package main

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"unicode"

	"github.com/majd/ipatool/v2/pkg/appstore"
)

const (
	protocolVersion      = 1
	maxWireBytes         = 1 << 20
	maxStateBytes        = 512 << 10
	maxCookies           = 512
	anisetteKey          = "anisette-v4"
	stageProtocol        = "storeauth-protocol"
	stageInput           = "storeauth-input"
	stageAccountMismatch = "storeauth-account-mismatch"
	stageAuthentication  = "storeauth-authentication"
	stageCodeRequired    = "storeauth-code-required"
	stageCodeInvalid     = "storeauth-code-invalid"
	stageCookies         = "storeauth-cookies"
	stageState           = "storeauth-state"
	stagePrepare         = "storeauth-prepare"
	stageInternal        = "storeauth-internal"
)

var (
	errProtocol       = errors.New("invalid helper protocol")
	dsidPattern       = regexp.MustCompile(`^[0-9]{1,32}$`)
	podPattern        = regexp.MustCompile(`^[0-9]{1,5}$`)
	storefrontPattern = regexp.MustCompile(`^[0-9]{6}(?:-[0-9]{1,8})?(?:,[0-9]{1,8}){0,8}$`)
)

type loginRequest struct {
	Version      int    `json:"version"`
	Type         string `json:"type"`
	Email        string `json:"email"`
	Password     string `json:"password"`
	MACAddress   string `json:"mac_address"`
	ExpectedDSID string `json:"expected_dsid"`
	State        []byte `json:"state,omitempty"`
}

type codeRequest struct {
	Version int    `json:"version"`
	Type    string `json:"type"`
	Code    string `json:"code"`
}

type cookieEvent struct {
	URL     string         `json:"url"`
	Cookies []*http.Cookie `json:"cookies"`
}

type loginResult struct {
	Version    int           `json:"version"`
	Type       string        `json:"type"`
	OK         bool          `json:"ok"`
	Email      string        `json:"email,omitempty"`
	DSID       string        `json:"dsid,omitempty"`
	Token      string        `json:"token,omitempty"`
	Storefront string        `json:"storefront,omitempty"`
	Pod        string        `json:"pod,omitempty"`
	GUID       string        `json:"guid,omitempty"`
	Cookies    []cookieEvent `json:"cookies,omitempty"`
	State      []byte        `json:"state,omitempty"`
	Stage      string        `json:"stage,omitempty"`
	HTTPStatus int           `json:"http_status,omitempty"`
}

type wire struct {
	scanner                 *bufio.Scanner
	output                  io.Writer
	readBytes, writtenBytes int
	finished                bool
}

func newWire(input io.Reader, output io.Writer) *wire {
	scanner := bufio.NewScanner(io.LimitReader(input, maxWireBytes+1))
	scanner.Buffer(make([]byte, 4096), maxWireBytes)
	return &wire{scanner: scanner, output: output}
}

func (w *wire) read(value any) error {
	if !w.scanner.Scan() {
		return errProtocol
	}
	data := w.scanner.Bytes()
	w.readBytes += len(data) + 1
	if w.readBytes > maxWireBytes || len(data) == 0 {
		return errProtocol
	}
	return decodeStrict(data, value)
}

func decodeStrict(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return errProtocol
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errProtocol
	}
	return nil
}

func (w *wire) write(value any) error {
	if w.finished {
		return errProtocol
	}
	data, err := json.Marshal(value)
	if err != nil || w.writtenBytes+len(data)+1 > maxWireBytes {
		return errProtocol
	}
	data = append(data, '\n')
	n, err := w.output.Write(data)
	w.writtenBytes += n
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	return err
}

func (w *wire) challenge() error {
	return w.write(struct {
		Version int    `json:"version"`
		Type    string `json:"type"`
	}{protocolVersion, "challenge"})
}

func (w *wire) result(result loginResult) error {
	result.Version, result.Type = protocolVersion, "result"
	err := w.write(result)
	if errors.Is(err, errProtocol) && !w.finished {
		small := failure(stageProtocol, nil)
		small.Version, small.Type = protocolVersion, "result"
		_ = w.write(small)
	}
	w.finished = true
	return err
}

type fixedMachine struct{ mac, guid string }

func (m fixedMachine) MacAddress() (string, error)    { return m.mac, nil }
func (fixedMachine) HomeDirectory() string            { return "" }
func (fixedMachine) ReadPassword(int) ([]byte, error) { return nil, errProtocol }

func validateLogin(request loginRequest) (fixedMachine, string) {
	if request.Version != protocolVersion || request.Type != "login" ||
		!validIdentity(request.Email) || len(request.Password) == 0 || len(request.Password) > 1024 ||
		!dsidPattern.MatchString(request.ExpectedDSID) {
		return fixedMachine{}, stageInput
	}
	mac, err := net.ParseMAC(request.MACAddress)
	if err != nil || len(mac) != 6 || bytes.Equal(mac, make([]byte, 6)) || mac[0]&1 != 0 {
		return fixedMachine{}, stageInput
	}
	if len(request.State) != 0 && !validState(request.State) {
		return fixedMachine{}, stageState
	}
	return fixedMachine{mac: mac.String(), guid: strings.ToUpper(hex.EncodeToString(mac))}, ""
}

func validIdentity(value string) bool {
	return len(value) > 0 && len(value) <= 320 && strings.IndexFunc(value, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r)
	}) == -1
}

func validateCode(request codeRequest) (string, bool) {
	if request.Version != protocolVersion || request.Type != "code" || len(request.Code) > 32 {
		return "", false
	}
	code := strings.ReplaceAll(strings.TrimSpace(request.Code), " ", "")
	if len(code) != 6 {
		return "", false
	}
	for _, digit := range code {
		if digit < '0' || digit > '9' {
			return "", false
		}
	}
	return code, true
}

func validatedResult(request loginRequest, machine fixedMachine, account appstore.Account) (loginResult, string) {
	if account.DirectoryServicesID != request.ExpectedDSID ||
		!validIdentity(account.Email) || !strings.EqualFold(account.Email, request.Email) {
		return loginResult{}, stageAccountMismatch
	}
	if account.PasswordToken == "" || len(account.PasswordToken) > 16384 ||
		strings.IndexFunc(account.PasswordToken, unicode.IsControl) != -1 ||
		!storefrontPattern.MatchString(account.StoreFront) ||
		(account.Pod != "" && !podPattern.MatchString(account.Pod)) {
		return loginResult{}, stageAuthentication
	}
	return loginResult{
		OK: true, Email: account.Email, DSID: account.DirectoryServicesID,
		Token: account.PasswordToken, Storefront: account.StoreFront,
		Pod: account.Pod, GUID: machine.guid,
	}, ""
}

func validState(data []byte) bool {
	if len(data) == 0 || len(data) > maxStateBytes {
		return false
	}
	var state struct {
		Identifier, Provisioning []byte
		RoutingInfo              uint64
	}
	return decodeStrict(data, &state) == nil && len(state.Identifier) == 16 && len(state.Provisioning) <= maxStateBytes
}

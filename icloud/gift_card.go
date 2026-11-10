package icloud

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const giftLanding = "https://buy.itunes.apple.com/WebObjects/MZFinance.woa/wa/redeemLandingPage"

// GiftCardInfo never contains a redemption code, Apple token or raw response.
type GiftCardInfo struct {
	Ready         bool   `json:"ready"`
	PreparationID string `json:"preparation_id,omitempty"`
	State         string `json:"state,omitempty"`
	Credited      string `json:"credited,omitempty"`
	Balance       string `json:"balance,omitempty"`
	AppleCode     string `json:"apple_code,omitempty"`
}
type giftCardState struct {
	endpoint, nonce, email string
	expires                time.Time
	attempts               map[[32]byte]bool
}

func giftFailure(stage string) error { return &StorageError{Stage: "gift-" + stage} }

// giftRequest deliberately does not use diagnostic response capture. Neither
// a code nor an authenticated response is logged, saved, or automatically replayed.
func (s *StoreCommerceSession) giftRequest(ctx context.Context, method, endpoint string, body []byte) ([]byte, error) {
	if s == nil || s.identity == nil || s.client == nil || !s.MatchesAccount(s.identity.DSID) {
		return nil, giftFailure("session")
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, giftFailure("request")
	}
	s.identity.setHeaders(req)
	req.Header.Set("User-Agent", s.commerceUserAgent)
	req.Header.Set("Accept", "application/json, text/html")
	if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	client := *s.client
	client.Timeout = 60 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return nil, giftFailure("transport")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxStoreBagBytes+1))
	if err != nil || len(data) > maxStoreBagBytes {
		return nil, giftFailure("response")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &StorageError{Stage: "gift-response", HTTPStatus: resp.StatusCode}
	}
	return data, nil
}

// parseGiftLanding follows the live desktop-finance-redeemcode.js contract:
// submit-url belongs to redeemForm (not the separate self-service form).
func parseGiftLanding(data []byte, email string) (string, error) {
	if email == "" {
		return "", giftFailure("account")
	}
	doc, err := html.Parse(bytes.NewReader(data))
	if err != nil {
		return "", giftFailure("page")
	}
	var endpoint string
	var fields, forms int
	var text strings.Builder
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inForm bool) {
		attrs := map[string]string{}
		for _, a := range n.Attr {
			attrs[a.Key] = a.Val
		}
		if attrs["id"] == "redeemForm" {
			forms++
			inForm = true
			endpoint = attrs["submit-url"]
		}
		if inForm && attrs["id"] == "codeField" && n.Data == "input" {
			fields++
		}
		if n.Type == html.TextNode && n.Parent != nil && n.Parent.Data != "script" && n.Parent.Data != "style" {
			text.WriteString(n.Data)
			text.WriteByte(' ')
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inForm)
		}
	}
	walk(doc, false)
	accountMatch := false
	for _, address := range regexp.MustCompile(`[A-Za-z0-9.!#$%&'*+/=?^_`+"`"+`{|}~-]+@[A-Za-z0-9.-]+`).FindAllString(text.String(), -1) {
		if strings.EqualFold(strings.TrimRight(address, "."), email) {
			accountMatch = true
		}
	}
	u, err := url.Parse(endpoint)
	if forms != 1 || fields != 1 || !accountMatch || err != nil || u.Scheme != "https" || u.Host != "buy.itunes.apple.com" || u.User != nil || u.Fragment != "" || u.RawQuery != "" || u.RawPath != "" {
		return "", giftFailure("page-binding")
	}
	if u.Path != "/commerce/redeemCodeSrv" && u.Path != "/WebObjects/MZFinance.woa/wa/redeemCodeSrv" {
		return "", giftFailure("endpoint")
	}
	return u.String(), nil
}

// PrepareGiftCard checks the actual signed-in redemption page without a code.
// It establishes the page's cookies, binds its account and issues a short-lived
// one-use confirmation ID. It is not a card validity or balance query.
func (s *StoreCommerceSession) PrepareGiftCard(ctx context.Context) (*GiftCardInfo, error) {
	if s == nil {
		return nil, giftFailure("session")
	}
	s.giftMu.Lock()
	defer s.giftMu.Unlock()
	if s.gift != nil {
		s.gift.nonce = ""
	}
	data, err := s.giftRequest(ctx, http.MethodGet, giftLanding, nil)
	if err != nil {
		return nil, err
	}
	endpoint, err := parseGiftLanding(data, s.frameworkEmail)
	if err != nil {
		return nil, err
	}
	var nonce [24]byte
	if _, err = rand.Read(nonce[:]); err != nil {
		return nil, giftFailure("prepare")
	}
	if s.gift == nil {
		s.gift = &giftCardState{attempts: map[[32]byte]bool{}}
	}
	s.gift.endpoint, s.gift.email = endpoint, s.frameworkEmail
	s.gift.nonce = hex.EncodeToString(nonce[:])
	s.gift.expires = time.Now().Add(5 * time.Minute)
	return &GiftCardInfo{Ready: true, PreparationID: s.gift.nonce, State: "prepared"}, nil
}

func (s *StoreCommerceSession) RedeemGiftCard(ctx context.Context, confirmation, code string) (*GiftCardInfo, error) {
	if s == nil {
		return nil, giftFailure("session")
	}
	s.giftMu.Lock()
	defer s.giftMu.Unlock()
	code = strings.ToUpper(strings.TrimSpace(code))
	if !regexp.MustCompile(`^X[A-Z0-9]{15}$`).MatchString(code) {
		return nil, giftFailure("code-format")
	}
	g := s.gift
	if g == nil || confirmation == "" || confirmation != g.nonce || time.Now().After(g.expires) || g.email != s.frameworkEmail {
		return nil, giftFailure("confirmation")
	}
	digest := sha256.Sum256([]byte(code))
	if g.attempts[digest] {
		return nil, giftFailure("already-submitted")
	}
	if s.identity == nil || !s.MatchesAccount(s.identity.DSID) {
		return nil, giftFailure("session")
	}
	// Consume BEFORE sending. Timeouts, redirects and Apple dialogs never retry.
	g.nonce = ""
	g.attempts[digest] = true
	form := url.Values{"code": {code}, "dsPersonId": {s.identity.DSID}, "response-content-type": {"application/json"}, "cameraRecognizedCode": {"false"}, "has4GBLimit": {"false"}, "attemptCount": {"1"}, "guid": {s.identity.GUID}, "cl": {"Configurator"}, "pg": {""}}
	data, err := s.giftRequest(ctx, http.MethodPost, g.endpoint, []byte(form.Encode()))
	if err != nil {
		return &GiftCardInfo{State: "unknown"}, err
	}
	return parseGiftResult(data, g.email), nil
}

func parseGiftResult(data []byte, email string) *GiftCardInfo {
	result := &GiftCardInfo{State: "unknown"}
	var response struct {
		Status      *int            `json:"status"`
		ErrorNumber json.RawMessage `json:"errorNumber"`
		Email       string          `json:"email"`
		Redeemed    *struct {
			Money string `json:"money"`
		} `json:"redeemedCredit"`
		Total *struct {
			Money string `json:"money"`
		} `json:"totalCredit"`
		Salables json.RawMessage `json:"salables"`
	}
	if json.Unmarshal(data, &response) != nil || response.Status == nil {
		return result
	}
	if len(response.ErrorNumber) > 0 && string(response.ErrorNumber) != "null" {
		n := strings.Trim(string(response.ErrorNumber), `"`)
		if regexp.MustCompile(`^[0-9]{1,6}$`).MatchString(n) {
			result.AppleCode = n
		}
		result.State = "rejected"
		return result
	}
	if *response.Status != 0 {
		result.State = "confirmation-required"
		return result
	}
	if response.Email != "" && !strings.EqualFold(response.Email, email) {
		return result
	}
	if len(response.Salables) > 0 && string(response.Salables) != "null" {
		result.State = "confirmation-required"
		return result
	}
	money := regexp.MustCompile(`^(?:[A-Z]{3}|US\$|[$€£¥￥])?[ \x{00a0}]*[0-9]+(?:[.,][0-9]+)*(?:[ \x{00a0}]+[A-Z]{3})?$`)
	if response.Redeemed == nil || response.Total == nil || len(response.Redeemed.Money) > 48 || len(response.Total.Money) > 48 || !money.MatchString(response.Redeemed.Money) || !money.MatchString(response.Total.Money) {
		return result
	}
	result.State = "completed"
	result.Credited = response.Redeemed.Money
	result.Balance = response.Total.Money
	return result
}

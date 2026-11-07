package sap

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"testing"

	"howett.net/plist"
)

type testRoundTripper func(*http.Request) (*http.Response, error)

func (f testRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSetupSharesStoreTransportAndCookies(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	var paths []string
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, Transport: testRoundTripper(func(req *http.Request) (*http.Response, error) {
		paths = append(paths, req.URL.Path)
		header := http.Header{}
		key := setupBufferKey
		if req.URL.Path == "/certificate" {
			key = setupCertificateKey
			header.Set("Set-Cookie", "fixture=value; Path=/; Secure")
		} else if cookie, err := req.Cookie("fixture"); err != nil || cookie.Value != "value" {
			t.Fatal("setup/login cookie jar was replaced")
		}
		body, _ := plist.Marshal(map[string]any{key: []byte("synthetic-sap-buffer")}, plist.XMLFormat)
		return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(string(body))), Request: req}, nil
	})}
	protocol := setupProtocol{client: setupClient(Config{Client: client})}
	if _, err := protocol.certificate(context.Background(), "https://buy.itunes.apple.com/certificate"); err != nil {
		t.Fatal(err)
	}
	if _, err := protocol.exchange(context.Background(), "https://buy.itunes.apple.com/setup", []byte("synthetic-input")); err != nil {
		t.Fatal(err)
	}
	response, err := client.Get("https://buy.itunes.apple.com/authenticate")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if len(paths) != 3 {
		t.Fatal("a request escaped the configured transport")
	}
}

func TestDefaultSetupDoesNotInheritEnvironmentProxy(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	client := setupClient(Config{})
	defer client.CloseIdleConnections()
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.Proxy != nil {
		t.Fatal("SAP setup uses an implicit proxy")
	}
	if client.CheckRedirect == nil {
		t.Fatal("redirects can leave the matched endpoint")
	}
}

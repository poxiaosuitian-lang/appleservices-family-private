package hmesync

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInvalidSyncKeysNeverSendRequests(t *testing.T) {
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ }))
	defer srv.Close()
	for _, token := range []string{"", "12345", "123 56", "123\n56", strings.Repeat("x", 513)} {
		ctx := context.Background()
		if _, err := importWithClient(ctx, srv.URL, token, Payload{}, srv.Client()); err == nil {
			t.Fatal("import accepted invalid key")
		}
		if _, err := loginCall(ctx, srv.Client(), srv.URL, token, loginRequest{}); err == nil {
			t.Fatal("login accepted invalid key")
		}
		client := NewRecoveryClient()
		if err := client.call(ctx, srv.URL, token, "wait", RecoveryRequest{}, &struct{}{}); err == nil {
			t.Fatal("recovery accepted invalid key")
		}
	}
	if calls != 0 {
		t.Fatal("invalid key sent to server")
	}
}

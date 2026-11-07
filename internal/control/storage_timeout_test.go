package control

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

func TestStorageAuthenticationAndPreparationReceiveFullDeadline(t *testing.T) {
	for _, action := range []string{"storage-skill-auth-local", "storage-purchase-prepare-us"} {
		t.Run(action, func(t *testing.T) {
			server, err := New(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer server.Close()
			remaining := make(chan time.Duration, 1)
			go func() {
				command := <-server.Commands
				deadline, _ := command.Context.Deadline()
				remaining <- time.Until(deadline)
				command.Reply <- Result{OK: true}
			}()
			req, _ := http.NewRequest(http.MethodPost, server.Record.URL+"/action", bytes.NewBufferString(`{"action":"`+action+`"}`))
			req.Header.Set("X-Control-Token", server.Record.Token)
			response, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 200 {
				t.Fatalf("action not delivered: %d", response.StatusCode)
			}
			if left := <-remaining; left < 170*time.Second || left > 180*time.Second {
				t.Fatalf("worker deadline cut short: %v", left)
			}
		})
	}
}

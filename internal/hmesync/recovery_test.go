package hmesync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoveryWaitOnlyReturnsBoundJobs(t *testing.T) {
	for _, mode := range []string{"idle", "job", "web", "wrong-account", "wrong-kind", "bad-lease", "redirect", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			source := SourceID("owner@example.test")
			calls := 0
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != RecoveryPath+"wait" {
					t.Error("redirect or unexpected operation")
				}
				var input RecoveryRequest
				if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Sources) != 1 || input.Sources[0] != source {
					t.Error("unbound request")
				}
				if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("k", 32) {
					t.Error("missing token")
				}
				if mode == "redirect" {
					w.Header().Set("Location", "/unexpected")
					w.WriteHeader(307)
					return
				}
				if mode == "unsupported" {
					w.WriteHeader(404)
					return
				}
				var job *RecoveryJob
				if mode != "idle" {
					job = &RecoveryJob{ID: "job_fixture", SourceID: source, Kind: "apple_login", Lease: strings.Repeat("a", 64)}
				}
				switch mode {
				case "web":
					job.Kind = "web_session"
				case "wrong-account":
					job.SourceID = SourceID("other@example.test")
				case "wrong-kind":
					job.Kind = "create_alias"
				case "bad-lease":
					job.Lease = "bad"
				}
				json.NewEncoder(w).Encode(map[string]any{"success": true, "data": map[string]any{"job": job}})
			}))
			defer srv.Close()
			c := &RecoveryClient{srv.Client()}
			c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			job, err := c.Wait(context.Background(), srv.URL, strings.Repeat("k", 32), "local-fixture", []string{source})
			if mode == "idle" {
				if err != nil || job != nil {
					t.Fatal("idle queue triggered work")
				}
			} else if mode == "job" || mode == "web" {
				if err != nil || job == nil {
					t.Fatal("valid job rejected")
				}
			} else if err == nil {
				t.Fatal("unsafe job accepted")
			}
			if calls != 1 {
				t.Fatal("retried request")
			}
		})
	}
}

func TestRecoveryCompletionContainsOnlyOutcome(t *testing.T) {
	job := RecoveryJob{ID: "job1", SourceID: SourceID("owner@example.test"), Lease: strings.Repeat("a", 64), Kind: "apple_login"}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var fields map[string]json.RawMessage
		json.NewDecoder(r.Body).Decode(&fields)
		for key := range fields {
			switch key {
			case "version", "client_id", "job_id", "lease", "outcome":
			default:
				t.Errorf("unexpected completion field %s", key)
			}
		}
		if r.URL.Path != RecoveryPath+"complete" {
			t.Error("unexpected endpoint")
		}
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	client := &RecoveryClient{server.Client()}
	if err := client.Complete(context.Background(), server.URL, strings.Repeat("k", 32), "local", job, "ready"); err != nil {
		t.Fatal(err)
	}
	if err := client.Complete(context.Background(), server.URL, strings.Repeat("k", 32), "local", job, "secret"); err == nil {
		t.Fatal("unrestricted outcome")
	}
}

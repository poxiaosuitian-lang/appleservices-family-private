package icloud

import (
	"bytes"
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Laky-64/appleservices/internal/srp"
)

// Compute proofs from the server's verifier and private ephemeral, independently
// of the production client/key helper. Only synthetic credentials are used.
func webServerProofs(protocol string, a *big.Int) (salt, bPublic, m1, m2 []byte) {
	hash := func(parts ...[]byte) []byte {
		h := sha256.New()
		for _, p := range parts {
			h.Write(p)
		}
		return h.Sum(nil)
	}
	p := srp.NewParams()
	pad := func(n *big.Int) []byte { return n.FillBytes(make([]byte, 256)) }
	salt = []byte("synthetic-salt")
	input := hash([]byte("synthetic-password"))
	if protocol == "s2k_fo" {
		input = []byte(hex.EncodeToString(input))
	}
	key, _ := pbkdf2.Key(sha256.New, string(input), salt, 1000, 32)
	x := new(big.Int).SetBytes(hash(salt, hash([]byte(":"), key)))
	v := new(big.Int).Exp(p.G, x, p.N)
	k := new(big.Int).SetBytes(hash(pad(p.N), pad(p.G)))
	b := big.NewInt(11731)
	B := new(big.Int).Mod(new(big.Int).Add(new(big.Int).Mul(k, v), new(big.Int).Exp(p.G, b, p.N)), p.N)
	u := new(big.Int).SetBytes(hash(pad(a), pad(B)))
	S := new(big.Int).Exp(new(big.Int).Mul(a, new(big.Int).Exp(v, u, p.N)), b, p.N)
	K := hash(S.Bytes())
	xor := hash(pad(p.N))
	hg := hash(pad(p.G))
	for i := range xor {
		xor[i] ^= hg[i]
	}
	m1 = hash(xor, hash([]byte("owner@example.test")), salt, a.Bytes(), B.Bytes(), K)
	m2 = hash(a.Bytes(), m1, K)
	return salt, B.Bytes(), m1, m2
}

func TestWebAuthenticationNegotiatesProtocolAndKeepsRequestContext(t *testing.T) {
	for _, protocol := range []string{"s2k", "s2k_fo", "unsupported"} {
		t.Run(protocol, func(t *testing.T) {
			var frame, fingerprint, origin string
			var wantM1, wantM2 []byte
			completes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/auth/authorize/signin" {
					frame = r.URL.Query().Get("state")
					if frame == "" || frame != r.URL.Query().Get("frame_id") || frame != r.URL.Query().Get("iframeId") {
						t.Error("authorize frame mismatch")
					}
					w.Header().Set("scnt", "synthetic-context")
					w.Write([]byte(`{}`))
					return
				}
				if r.Header.Get("Origin") != origin || r.Header.Get("X-Apple-OAuth-State") != frame || r.Header.Get("X-Apple-Frame-Id") != frame || r.Header.Get("scnt") != "synthetic-context" {
					t.Error("authentication context changed")
				}
				fd := r.Header.Get("X-Apple-I-FD-Client-Info")
				if fingerprint == "" {
					fingerprint = fd
				}
				if fd == "" || fd != fingerprint {
					t.Error("fingerprint changed within handshake")
				}
				switch r.URL.Path {
				case "/auth/federate":
					w.Write([]byte(`{}`))
				case "/auth/signin/init":
					var req struct{ A string }
					if json.NewDecoder(r.Body).Decode(&req) != nil {
						t.Error("invalid init")
						http.Error(w, "", 400)
						return
					}
					A, e := base64.StdEncoding.DecodeString(req.A)
					if e != nil || len(A) == 0 {
						t.Error("invalid public key")
						http.Error(w, "", 400)
						return
					}
					salt, B, m1, m2 := webServerProofs(protocol, new(big.Int).SetBytes(A))
					wantM1, wantM2 = m1, m2
					json.NewEncoder(w).Encode(map[string]any{"iteration": 1000, "salt": base64.StdEncoding.EncodeToString(salt), "b": base64.StdEncoding.EncodeToString(B), "c": "synthetic-challenge", "protocol": protocol})
				case "/auth/signin/complete":
					completes++
					var req struct{ M1, M2, C string }
					if json.NewDecoder(r.Body).Decode(&req) != nil {
						t.Error("invalid complete")
					}
					m1, _ := base64.StdEncoding.DecodeString(req.M1)
					m2, _ := base64.StdEncoding.DecodeString(req.M2)
					if !bytes.Equal(m1, wantM1) || !bytes.Equal(m2, wantM2) || req.C != "synthetic-challenge" {
						t.Error("client proof does not match independent server")
					}
					// End after proof validation; no real account session or OTP.
					w.WriteHeader(401)
					w.Write([]byte(`{"serviceErrors":[{"code":"-20101"}],"private":"must-not-leak"}`))
				default:
					t.Error("unexpected authentication continuation")
					http.Error(w, "", 500)
				}
			}))
			defer server.Close()
			origin = server.URL
			c := &HideMyEmailClient{appleID: "owner@example.test", serviceName: "mccgateway", authBase: server.URL + "/auth", sessionPath: filepath.Join(t.TempDir(), "session.json")}
			err := c.Authenticate(context.Background(), "synthetic-password", nil)
			if err == nil || strings.Contains(err.Error(), "must-not-leak") {
				t.Fatal("invalid or unsafe authentication result")
			}
			if protocol == "unsupported" {
				if completes != 0 {
					t.Fatal("unknown protocol submitted a proof")
				}
			} else if completes != 1 {
				t.Fatal("proof not submitted exactly once")
			}
		})
	}
}

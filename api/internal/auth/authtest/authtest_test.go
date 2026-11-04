package authtest

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/marboleda/shelfkin/api/internal/auth"
)

func TestVerifier(t *testing.T) {
	v := NewVerifier()
	alice := auth.Token{UID: "alice", Email: "alice@example.com", Name: "Alice"}
	t1, t2 := v.Issue(alice), v.Issue(alice)
	if t1 == t2 {
		t.Error("Issue returned the same token twice")
	}
	for _, tok := range []string{t1, t2} {
		got, err := v.Verify(context.Background(), tok)
		if err != nil || *got != alice {
			t.Errorf("Verify(%q) = %+v, %v; want %+v", tok, got, err, alice)
		}
	}
	if _, err := v.Verify(context.Background(), "unknown"); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("unknown token: err = %v, want ErrInvalidToken", err)
	}
	v.Revoke(t1)
	if _, err := v.Verify(context.Background(), t1); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("revoked token: err = %v, want ErrInvalidToken", err)
	}
	if _, err := v.Verify(context.Background(), t2); err != nil {
		t.Errorf("revoking one token affected another: %v", err)
	}
	// Another Verifier does not accept this one's tokens.
	if _, err := NewVerifier().Verify(context.Background(), t2); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("token from another Verifier: err = %v", err)
	}
	if got := Bearer("abc"); got != "Bearer abc" {
		t.Errorf("Bearer = %q", got)
	}
}

func TestUnsignedToken(t *testing.T) {
	tok := UnsignedToken(map[string]any{"sub": "u", "aud": "p"})
	parts := strings.Split(tok, ".")
	if len(parts) != 3 || parts[2] != "" {
		t.Fatalf("token %q is not header.payload.(empty signature)", tok)
	}
	var header, claims map[string]any
	for i, out := range []*map[string]any{&header, &claims} {
		b, err := base64.RawURLEncoding.DecodeString(parts[i])
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, out); err != nil {
			t.Fatal(err)
		}
	}
	if header["alg"] != "none" || claims["sub"] != "u" || claims["aud"] != "p" {
		t.Errorf("header %v claims %v", header, claims)
	}
}

// Package authtest lets API tests authenticate as a test user without the
// Firebase Auth emulator.
//
// Build the server with a Verifier from this package instead of the Firebase
// one, issue a token for each test user and send it as the bearer token:
//
//	v := authtest.NewVerifier()
//	h := server.NewHandler(server.NewAPI(pool, logger), auth.NewAuthenticator(v, pool), logger, nil)
//	token := v.Issue(auth.Token{UID: "alice", Email: "alice@example.com", Name: "Alice"})
//	req.Header.Set("Authorization", authtest.Bearer(token))
//
// The first request with a token creates the users row exactly as a real
// Firebase token would. Any token the Verifier did not issue is rejected with
// auth.ErrInvalidToken.
package authtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"

	"github.com/marboleda/shelfkin/api/internal/auth"
)

// Verifier is an auth.Verifier that accepts only the tokens it issued.
// It is safe for concurrent use.
type Verifier struct {
	mu     sync.Mutex
	tokens map[string]auth.Token
}

var _ auth.Verifier = (*Verifier)(nil)

// NewVerifier returns a Verifier with no tokens.
func NewVerifier() *Verifier {
	return &Verifier{tokens: map[string]auth.Token{}}
}

// Issue returns a new opaque token that Verify maps to t. t.UID must not be
// empty. Issuing several tokens for the same UID is fine; they all
// authenticate as the same user.
func (v *Verifier) Issue(t auth.Token) string {
	if t.UID == "" {
		panic("authtest: Issue needs a UID")
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	token := "authtest-" + hex.EncodeToString(b)
	v.mu.Lock()
	defer v.mu.Unlock()
	v.tokens[token] = t
	return token
}

// Revoke makes Verify reject token from now on, as if it had expired.
func (v *Verifier) Revoke(token string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	delete(v.tokens, token)
}

// Verify returns the Token that token was issued for, or an error wrapping
// auth.ErrInvalidToken.
func (v *Verifier) Verify(_ context.Context, token string) (*auth.Token, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	t, ok := v.tokens[token]
	if !ok {
		return nil, fmt.Errorf("%w: not issued by authtest.Verifier", auth.ErrInvalidToken)
	}
	return &t, nil
}

// Bearer returns the Authorization header value for token.
func Bearer(token string) string {
	return "Bearer " + token
}

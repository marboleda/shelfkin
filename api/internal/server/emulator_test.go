package server

// Integration tests against the Firebase Auth emulator (compose service
// `auth`, started by `npm run db:up`; CI starts it too). They use the real
// FirebaseVerifier, never the authtest fake, and fail rather than skip if the
// emulator is not running.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/marboleda/shelfkin/api/db/dbtest"
	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/auth/authtest"
)

// emulatorServer serves the real API on a fresh database, verifying tokens
// with the Firebase Admin SDK against the emulator for project.
func emulatorServer(t *testing.T, host, project string) *meServer {
	t.Helper()
	// The Admin SDK reads the emulator host only from the environment.
	t.Setenv(auth.EmulatorHostEnv, host)
	verifier, err := auth.NewFirebaseVerifier(context.Background(), project, host)
	if err != nil {
		t.Fatal(err)
	}
	pool := dbtest.New(t)
	logger, _ := newLogger()
	url := serve(t, NewHandler(NewAPI(pool, logger), auth.NewAuthenticator(verifier, pool), logger, nil))
	return &meServer{url: url, pool: pool}
}

func uniqueEmail(t *testing.T, prefix string) string {
	t.Helper()
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return prefix + "-" + hex.EncodeToString(b) + "@example.com"
}

func (s *meServer) countUsers(t *testing.T, firebaseUID string) int {
	t.Helper()
	var n int
	if err := s.pool.QueryRow(context.Background(), "SELECT count(*) FROM users WHERE firebase_uid = $1", firebaseUID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEmulatorValidTokenCreatesUserOnce(t *testing.T) {
	host := authtest.EmulatorHost(t)
	s := emulatorServer(t, host, authtest.EmulatorProjectID)
	email := uniqueEmail(t, "sam")
	user := authtest.SignUp(t, host, email, "password123", "Sam Emulator")

	u := s.user(t, http.MethodGet, user.IDToken, "")
	if u["displayName"] != "Sam Emulator" || u["email"] != email || u["timezone"] != "UTC" || u["pushEnabled"] != false {
		t.Errorf("GET /me = %v", u)
	}
	again := s.user(t, http.MethodGet, user.IDToken, "")
	if again["id"] != u["id"] {
		t.Errorf("second GET /me id = %v, want %v", again["id"], u["id"])
	}
	if n := s.countUsers(t, user.UID); n != 1 {
		t.Errorf("%d users rows for the Firebase user, want 1", n)
	}
}

func TestEmulatorDisplayNameFallbacks(t *testing.T) {
	host := authtest.EmulatorHost(t)
	s := emulatorServer(t, host, authtest.EmulatorProjectID)

	email := uniqueEmail(t, "nameless")
	noName := authtest.SignUp(t, host, email, "password123", "")
	if u := s.user(t, http.MethodGet, noName.IDToken, ""); u["displayName"] != strings.Split(email, "@")[0] {
		t.Errorf("no name claim: displayName = %v, want the email's local part", u["displayName"])
	}

	anon := authtest.SignUp(t, host, "", "", "")
	if u := s.user(t, http.MethodGet, anon.IDToken, ""); u["displayName"] != "Shelfkin user" || u["email"] != nil {
		t.Errorf("anonymous user: %v, want displayName Shelfkin user and email null", u)
	}
}

func TestEmulatorEmailChangeIsCopied(t *testing.T) {
	host := authtest.EmulatorHost(t)
	s := emulatorServer(t, host, authtest.EmulatorProjectID)
	user := authtest.SignUp(t, host, uniqueEmail(t, "before"), "password123", "Keep Me")
	s.user(t, http.MethodGet, user.IDToken, "")

	newEmail := uniqueEmail(t, "after")
	token := authtest.UpdateEmail(t, host, user.IDToken, newEmail)
	u := s.user(t, http.MethodGet, token, "")
	if u["email"] != newEmail || u["displayName"] != "Keep Me" {
		t.Errorf("after an email change: %v, want email %s and the original name", u, newEmail)
	}
}

func TestEmulatorRejectedTokens(t *testing.T) {
	host := authtest.EmulatorHost(t)
	s := emulatorServer(t, host, authtest.EmulatorProjectID)
	user := authtest.SignUp(t, host, uniqueEmail(t, "reject"), "password123", "")

	now := time.Now().Unix()
	claims := func(project string, iat, exp int64) map[string]any {
		return map[string]any{
			"iss":       "https://securetoken.google.com/" + project,
			"aud":       project,
			"sub":       user.UID,
			"user_id":   user.UID,
			"auth_time": iat,
			"iat":       iat,
			"exp":       exp,
		}
	}
	// The control: the same hand-built form with valid claims is accepted,
	// so the rejections below are about the claims that differ. In emulator
	// mode the Admin SDK treats a token issued before the user's
	// validSince (its creation, in milliseconds) as revoked, so iat is a
	// little in the future, well inside the SDK's 5-minute clock skew.
	control := authtest.UnsignedToken(claims(authtest.EmulatorProjectID, now+2, now+3600))
	s.user(t, http.MethodGet, control, "")

	tests := []struct {
		name  string
		token string
	}{
		{"expired", authtest.UnsignedToken(claims(authtest.EmulatorProjectID, now-7200, now-3600))},
		{"another emulator project", authtest.UnsignedToken(claims("demo-other", now+2, now+3600))},
		{"garbage", "this-is-not-a-jwt"},
		{"three garbage segments", "aaa.bbb.ccc"},
		{"truncated real token", user.IDToken[:len(user.IDToken)/2]},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, body := s.call(t, http.MethodGet, tt.token, "")
			assertError(t, resp, body, http.StatusUnauthorized, CodeUnauthorized)
			if strings.Contains(string(body), tt.token) {
				t.Errorf("401 body echoes the token: %s", body)
			}
		})
	}

	t.Run("missing header", func(t *testing.T) {
		resp, body := do(t, http.MethodGet, s.url+"/me")
		assertError(t, resp, body, http.StatusUnauthorized, CodeUnauthorized)
	})

	// None of the rejected requests created a user; the control created one.
	var n int
	if err := s.pool.QueryRow(context.Background(), "SELECT count(*) FROM users").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("%d users rows, want only the control's", n)
	}
}

// TestEmulatorTokenFromAnotherProject: a real emulator token for
// demo-shelfkin, sent to a server configured for another project.
func TestEmulatorTokenFromAnotherProject(t *testing.T) {
	host := authtest.EmulatorHost(t)
	s := emulatorServer(t, host, "demo-another-project")
	user := authtest.SignUp(t, host, uniqueEmail(t, "elsewhere"), "password123", "")
	resp, body := s.call(t, http.MethodGet, user.IDToken, "")
	assertError(t, resp, body, http.StatusUnauthorized, CodeUnauthorized)
	if n := s.countUsers(t, user.UID); n != 0 {
		t.Errorf("a rejected token created %d users", n)
	}
}

func TestEmulatorPatchMe(t *testing.T) {
	host := authtest.EmulatorHost(t)
	s := emulatorServer(t, host, authtest.EmulatorProjectID)
	user := authtest.SignUp(t, host, uniqueEmail(t, "patch"), "password123", "Pat")
	u := s.user(t, http.MethodPatch, user.IDToken, `{"displayName":"Patricia","timezone":"America/Toronto"}`)
	if u["displayName"] != "Patricia" || u["timezone"] != "America/Toronto" {
		t.Errorf("PATCH /me = %v", u)
	}
}

package authtest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// Emulator settings. The compose service `auth` runs the Firebase Auth
// emulator for project EmulatorProjectID on host port SHELFKIN_AUTH_PORT
// (default 9099).
const (
	// EmulatorProjectID is the demo project the local emulator runs. The
	// demo- prefix means it can never reach a real Firebase project.
	EmulatorProjectID = "demo-shelfkin"
	// EmulatorPortEnv overrides the host port of the compose emulator.
	EmulatorPortEnv = "SHELFKIN_AUTH_PORT"
	// EmulatorHostTestEnv names the emulator (host:port) that tests use,
	// overriding the compose one.
	EmulatorHostTestEnv = "TEST_AUTH_EMULATOR_HOST"
)

// EmulatorHost returns the host:port of the Firebase Auth emulator for tests:
// TEST_AUTH_EMULATOR_HOST, else localhost with SHELFKIN_AUTH_PORT (default
// 9099). The test fails, and never skips, if the emulator does not answer.
func EmulatorHost(t testing.TB) string {
	t.Helper()
	host := os.Getenv(EmulatorHostTestEnv)
	source := EmulatorHostTestEnv
	if host == "" {
		port := os.Getenv(EmulatorPortEnv)
		if port == "" {
			port = "9099"
		}
		host = "localhost:" + port
		source = "the local compose emulator"
	}
	client := http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://" + host + "/")
	if err == nil {
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("status %d", resp.StatusCode)
		}
	}
	if err != nil {
		t.Fatalf("authtest: the Firebase Auth emulator at %s (%s) is not answering: %v\n"+
			"Start it with `npm run db:up`, or set %s.", host, source, err, EmulatorHostTestEnv)
	}
	return host
}

// EmulatorUser is a user created in the emulator.
type EmulatorUser struct {
	UID     string
	IDToken string
}

// SignUp creates a user in the emulator at host and returns its UID and a
// fresh ID token for project EmulatorProjectID. With an empty email it
// creates an anonymous user, whose token has no email. displayName may be
// empty.
func SignUp(t testing.TB, host, email, password, displayName string) EmulatorUser {
	t.Helper()
	body := map[string]any{"returnSecureToken": true}
	if email != "" {
		body["email"] = email
		body["password"] = password
	}
	if displayName != "" {
		body["displayName"] = displayName
	}
	var out struct {
		LocalID string `json:"localId"`
		IDToken string `json:"idToken"`
	}
	emulatorPost(t, host, "accounts:signUp", body, &out)
	if out.LocalID == "" || out.IDToken == "" {
		t.Fatalf("authtest: emulator sign-up returned no user or token")
	}
	return EmulatorUser{UID: out.LocalID, IDToken: out.IDToken}
}

// UpdateEmail changes the user's email in the emulator and returns a fresh
// ID token carrying the new email.
func UpdateEmail(t testing.TB, host, idToken, email string) string {
	t.Helper()
	var out struct {
		IDToken string `json:"idToken"`
	}
	emulatorPost(t, host, "accounts:update", map[string]any{
		"idToken": idToken, "email": email, "returnSecureToken": true,
	}, &out)
	if out.IDToken == "" {
		t.Fatalf("authtest: emulator email update returned no token")
	}
	return out.IDToken
}

// UnsignedToken builds an unsigned JWT (alg none), the form the emulator
// issues, with the given claims. Use it for tokens the emulator cannot
// produce, such as an expired one.
func UnsignedToken(claims map[string]any) string {
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			panic(err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	return enc(map[string]any{"alg": "none", "typ": "JWT"}) + "." + enc(claims) + "."
}

// emulatorPost calls an Identity Toolkit v1 endpoint of the emulator, which
// accepts any API key and serves the project it was started with.
func emulatorPost(t testing.TB, host, method string, body map[string]any, out any) {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	url := fmt.Sprintf("http://%s/identitytoolkit.googleapis.com/v1/%s?key=fake-api-key", host, method)
	client := http.Client{Timeout: 10 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("authtest: emulator %s: %v", method, err)
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("authtest: emulator %s: status %d: %s", method, resp.StatusCode, respBody)
	}
	if out != nil {
		if err := json.Unmarshal(respBody, out); err != nil {
			t.Fatalf("authtest: emulator %s: decoding %s: %v", method, respBody, err)
		}
	}
}

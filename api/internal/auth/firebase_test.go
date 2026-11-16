package auth_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/auth/authtest"
)

func TestNewFirebaseVerifierConfig(t *testing.T) {
	ctx := context.Background()
	t.Setenv(auth.EmulatorHostEnv, "")
	if _, err := auth.NewFirebaseVerifier(ctx, "", ""); err == nil {
		t.Error("empty project ID accepted")
	}
	// Production mode needs no credentials.
	if _, err := auth.NewFirebaseVerifier(ctx, "demo-test", ""); err != nil {
		t.Errorf("NewFirebaseVerifier: %v", err)
	}
	// The configured emulator host must match what the SDK will read.
	if _, err := auth.NewFirebaseVerifier(ctx, "demo-test", "localhost:9099"); err == nil {
		t.Error("emulator host configured but not in the environment: accepted")
	}
	t.Setenv(auth.EmulatorHostEnv, "localhost:9099")
	if _, err := auth.NewFirebaseVerifier(ctx, "demo-test", ""); err == nil {
		t.Error("emulator host in the environment but not configured: accepted")
	}
}

func TestFirebaseVerifierProductionRejectsEmulatorTokens(t *testing.T) {
	t.Setenv(auth.EmulatorHostEnv, "")
	v, err := auth.NewFirebaseVerifier(context.Background(), "demo-test", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	// An unsigned token with otherwise perfect claims: rejected before any
	// key fetch, because it has no kid and is not RS256.
	tok := authtest.UnsignedToken(map[string]any{
		"iss": "https://securetoken.google.com/demo-test", "aud": "demo-test",
		"sub": "u", "iat": now, "exp": now + 3600,
	})
	if _, err := v.Verify(context.Background(), tok); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("unsigned token without the emulator: err = %v, want ErrInvalidToken", err)
	}
}

func TestFirebaseVerifierEmulatorClaims(t *testing.T) {
	host := authtest.EmulatorHost(t)
	t.Setenv(auth.EmulatorHostEnv, host)
	v, err := auth.NewFirebaseVerifier(context.Background(), authtest.EmulatorProjectID, host)
	if err != nil {
		t.Fatal(err)
	}
	user := authtest.SignUp(t, host, "claims-"+time.Now().Format("150405.000000")+"@example.com", "password123", "Claire")
	tok, err := v.Verify(context.Background(), user.IDToken)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if tok.UID != user.UID || tok.Name != "Claire" || tok.Email == "" {
		t.Errorf("Verify = %+v, want UID %s, name Claire and the email", tok, user.UID)
	}
}

// TestFirebaseVerifierUnreachableEmulator: failing to reach Firebase is not
// the caller's fault, so it must not be reported as an invalid token (401).
func TestFirebaseVerifierUnreachableEmulator(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host := ln.Addr().String()
	ln.Close() // nothing listens here now
	t.Setenv(auth.EmulatorHostEnv, host)
	v, err := auth.NewFirebaseVerifier(context.Background(), "demo-test", host)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	tok := authtest.UnsignedToken(map[string]any{
		"iss": "https://securetoken.google.com/demo-test", "aud": "demo-test",
		"sub": "u", "iat": now, "exp": now + 3600,
	})
	// The SDK retries; a short deadline keeps the test fast.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = v.Verify(ctx, tok)
	if err == nil || errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("err = %v, want a non-token error", err)
	}
}

func TestNewFirebaseAccountDeleterConfig(t *testing.T) {
	t.Setenv(auth.EmulatorHostEnv, "")
	if _, err := auth.NewFirebaseAccountDeleter("", ""); err == nil {
		t.Error("empty project ID accepted")
	}
	// Production mode looks up credentials only when deleting, so the server
	// starts without them.
	if _, err := auth.NewFirebaseAccountDeleter("demo-test", ""); err != nil {
		t.Errorf("NewFirebaseAccountDeleter: %v", err)
	}
	if _, err := auth.NewFirebaseAccountDeleter("demo-test", "localhost:9099"); err == nil {
		t.Error("emulator host configured but not in the environment: accepted")
	}
	t.Setenv(auth.EmulatorHostEnv, "localhost:9099")
	if _, err := auth.NewFirebaseAccountDeleter("demo-test", ""); err == nil {
		t.Error("emulator host in the environment but not configured: accepted")
	}
}

// TestFirebaseAccountDeleterEmulator: DeleteUser deletes the Firebase user,
// after which its token is rejected, and deleting a user that is already gone
// succeeds (so DELETE /me can be retried).
func TestFirebaseAccountDeleterEmulator(t *testing.T) {
	host := authtest.EmulatorHost(t)
	t.Setenv(auth.EmulatorHostEnv, host)
	ctx := context.Background()
	v, err := auth.NewFirebaseVerifier(ctx, authtest.EmulatorProjectID, host)
	if err != nil {
		t.Fatal(err)
	}
	d, err := auth.NewFirebaseAccountDeleter(authtest.EmulatorProjectID, host)
	if err != nil {
		t.Fatal(err)
	}
	user := authtest.SignUp(t, host, "delete-"+time.Now().Format("150405.000000")+"@example.com", "password123", "Del")
	if _, err := v.Verify(ctx, user.IDToken); err != nil {
		t.Fatalf("Verify before deletion: %v", err)
	}
	if err := d.DeleteUser(ctx, user.UID); err != nil {
		t.Fatalf("DeleteUser: %v", err)
	}
	if _, err := v.Verify(ctx, user.IDToken); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("Verify after deletion: err = %v, want ErrInvalidToken", err)
	}
	if err := d.DeleteUser(ctx, user.UID); err != nil {
		t.Errorf("DeleteUser of a deleted user: %v, want nil", err)
	}
}

// TestFirebaseAccountDeleterUnreachable: a failure to reach Firebase is an
// error, so DELETE /me rolls back.
func TestFirebaseAccountDeleterUnreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host := ln.Addr().String()
	ln.Close()
	t.Setenv(auth.EmulatorHostEnv, host)
	d, err := auth.NewFirebaseAccountDeleter("demo-test", host)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := d.DeleteUser(ctx, "someone"); err == nil {
		t.Error("DeleteUser with Firebase unreachable: nil error")
	}
}

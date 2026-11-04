package auth

import (
	"context"
	"errors"
	"fmt"
	"os"

	firebase "firebase.google.com/go/v4"
	fbauth "firebase.google.com/go/v4/auth"
	"google.golang.org/api/option"
)

// EmulatorHostEnv is the environment variable the Firebase Admin SDK reads to
// accept tokens from the Firebase Auth emulator instead of real Firebase.
const EmulatorHostEnv = "FIREBASE_AUTH_EMULATOR_HOST"

// FirebaseVerifier verifies ID tokens with the Firebase Admin SDK.
type FirebaseVerifier struct {
	client *fbauth.Client
}

// NewFirebaseVerifier returns a verifier for ID tokens of projectID.
//
// emulatorHost (host:port) makes it accept tokens from the Firebase Auth
// emulator, which are unsigned; it must never be set in production. The Admin
// SDK only reads the FIREBASE_AUTH_EMULATOR_HOST environment variable, so
// emulatorHost must equal it: NewFirebaseVerifier returns an error rather
// than silently accept emulator tokens the caller did not ask for.
//
// It needs no credentials: verifying ID tokens only uses Google's public
// keys, which the SDK fetches and caches until they expire.
func NewFirebaseVerifier(ctx context.Context, projectID, emulatorHost string) (*FirebaseVerifier, error) {
	if projectID == "" {
		return nil, errors.New("auth: Firebase project ID is required")
	}
	if env := os.Getenv(EmulatorHostEnv); env != emulatorHost {
		return nil, fmt.Errorf("auth: %s is %q in the environment but %q was configured", EmulatorHostEnv, env, emulatorHost)
	}
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: projectID}, option.WithoutAuthentication())
	if err != nil {
		return nil, fmt.Errorf("auth: creating Firebase app: %w", err)
	}
	client, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: creating Firebase Auth client: %w", err)
	}
	return &FirebaseVerifier{client: client}, nil
}

// Verify checks idToken's signature (skipped for emulator tokens), audience,
// issuer and expiry. It does not check for revocation: ID tokens expire
// within an hour, and a revocation check would call Firebase on every
// request.
func (v *FirebaseVerifier) Verify(ctx context.Context, idToken string) (*Token, error) {
	t, err := v.client.VerifyIDToken(ctx, idToken)
	if err != nil {
		// In emulator mode the SDK also looks the user up, so a token for a
		// user deleted from the emulator fails with user-not-found.
		if fbauth.IsIDTokenInvalid(err) || fbauth.IsUserNotFound(err) {
			return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
		}
		return nil, fmt.Errorf("auth: verifying ID token: %w", err)
	}
	email, _ := t.Claims["email"].(string)
	name, _ := t.Claims["name"].(string)
	return &Token{UID: t.UID, Email: email, Name: name}, nil
}

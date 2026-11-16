package auth

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"

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

// AccountDeleter deletes a Firebase Auth user. DeleteUser returns nil when
// the user is deleted or did not exist, so a retried account deletion
// succeeds.
type AccountDeleter interface {
	DeleteUser(ctx context.Context, firebaseUID string) error
}

// FirebaseAccountDeleter deletes Firebase Auth users with the Admin SDK.
type FirebaseAccountDeleter struct {
	projectID string

	mu     sync.Mutex
	client *fbauth.Client
}

// NewFirebaseAccountDeleter returns a deleter for users of projectID.
//
// Unlike token verification, deleting users needs credentials. In production
// they are the Application Default Credentials: on Cloud Run, the runtime
// service account, which needs the Firebase Authentication Admin role
// (infra/iam.tf). With emulatorHost set (it must equal
// FIREBASE_AUTH_EMULATOR_HOST, as for NewFirebaseVerifier) the SDK talks to
// the emulator and uses no credentials.
//
// The Admin SDK client, and with it the credentials, is created on the first
// DeleteUser call (and again on the next call if that fails), so missing
// credentials do not stop the server from starting; DELETE /me then fails
// with 503 and deletes nothing.
func NewFirebaseAccountDeleter(projectID, emulatorHost string) (*FirebaseAccountDeleter, error) {
	if projectID == "" {
		return nil, errors.New("auth: Firebase project ID is required")
	}
	if env := os.Getenv(EmulatorHostEnv); env != emulatorHost {
		return nil, fmt.Errorf("auth: %s is %q in the environment but %q was configured", EmulatorHostEnv, env, emulatorHost)
	}
	return &FirebaseAccountDeleter{projectID: projectID}, nil
}

// authClient returns the Admin SDK client, creating it on first use. It is
// created with a context that is never cancelled, because the credentials
// keep it to refresh access tokens after the request that created it ends.
func (d *FirebaseAccountDeleter) authClient(ctx context.Context) (*fbauth.Client, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.client != nil {
		return d.client, nil
	}
	ctx = context.WithoutCancel(ctx)
	app, err := firebase.NewApp(ctx, &firebase.Config{ProjectID: d.projectID})
	if err != nil {
		return nil, fmt.Errorf("auth: creating Firebase app: %w", err)
	}
	client, err := app.Auth(ctx)
	if err != nil {
		return nil, fmt.Errorf("auth: creating Firebase Auth admin client: %w", err)
	}
	d.client = client
	return client, nil
}

// DeleteUser deletes the Firebase user firebaseUID. A user that does not
// exist counts as deleted.
func (d *FirebaseAccountDeleter) DeleteUser(ctx context.Context, firebaseUID string) error {
	client, err := d.authClient(ctx)
	if err != nil {
		return err
	}
	if err := client.DeleteUser(ctx, firebaseUID); err != nil && !fbauth.IsUserNotFound(err) {
		return fmt.Errorf("auth: deleting Firebase user: %w", err)
	}
	return nil
}

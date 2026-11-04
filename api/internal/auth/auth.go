// Package auth verifies Firebase ID tokens and maps them to Shelfkin users.
//
// The server's middleware calls Authenticator.Authenticate with the bearer
// token of each request that needs authentication. It verifies the token
// with a Verifier (Firebase in production, authtest.Verifier in tests), finds
// or creates the matching users row and returns its ID, which the middleware
// stores in the request context (WithUserID). Handlers read it with UserID
// and pass it to dbtx.WithUser; they never see the Firebase UID.
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/marboleda/shelfkin/api/internal/db"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
)

// ErrInvalidToken is returned (wrapped) when a token is missing, malformed,
// expired, for another project or otherwise not acceptable. The server
// answers it with 401.
var ErrInvalidToken = errors.New("auth: invalid ID token")

// Token is what the API uses from a verified Firebase ID token.
type Token struct {
	// UID is the Firebase user ID (the token's sub claim). Never empty.
	UID string
	// Email is the token's email claim, or "" if it has none.
	Email string
	// Name is the token's name claim, or "" if it has none.
	Name string
}

// Verifier verifies a Firebase ID token. It returns an error wrapping
// ErrInvalidToken when the token is not acceptable, and any other error when
// it could not decide (for example, Google's public keys could not be
// fetched).
type Verifier interface {
	Verify(ctx context.Context, idToken string) (*Token, error)
}

// DefaultDisplayName is the display name of a new user whose token has
// neither a name nor an email.
const DefaultDisplayName = "Shelfkin user"

// MaxDisplayNameLength is the most characters (runes) a display name may
// have. PATCH /me enforces it; DisplayName truncates to it.
const MaxDisplayNameLength = 50

// DisplayName returns the display name a new user starts with: the token's
// name, else the part of its email before the last "@", else
// DefaultDisplayName. Each candidate is trimmed and skipped if empty, and the
// result is cut to MaxDisplayNameLength characters.
func DisplayName(t Token) string {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		if at := strings.LastIndex(t.Email, "@"); at >= 0 {
			name = strings.TrimSpace(t.Email[:at])
		}
	}
	if name == "" {
		return DefaultDisplayName
	}
	if r := []rune(name); len(r) > MaxDisplayNameLength {
		name = strings.TrimSpace(string(r[:MaxDisplayNameLength]))
	}
	return name
}

// Authenticator turns ID tokens into Shelfkin user IDs.
type Authenticator struct {
	verifier Verifier
	db       dbtx.Beginner
}

// NewAuthenticator returns an Authenticator that verifies tokens with
// verifier and finds or creates users in db (normally the *pgxpool.Pool).
func NewAuthenticator(verifier Verifier, db dbtx.Beginner) *Authenticator {
	return &Authenticator{verifier: verifier, db: db}
}

// Authenticate verifies idToken and returns the ID of the matching users
// row, creating it the first time a Firebase user is seen (see
// DisplayName; timezone starts as UTC). On later calls it updates the stored
// email when the token's differs, and never changes the display name.
// Concurrent first calls for the same user create one row and all succeed.
//
// The error wraps ErrInvalidToken when the token is not acceptable.
func (a *Authenticator) Authenticate(ctx context.Context, idToken string) (pgtype.UUID, error) {
	tok, err := a.verifier.Verify(ctx, idToken)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if tok == nil || tok.UID == "" {
		return pgtype.UUID{}, fmt.Errorf("%w: no user ID", ErrInvalidToken)
	}
	email := pgtype.Text{String: tok.Email, Valid: tok.Email != ""}

	var id pgtype.UUID
	err = dbtx.WithoutUser(ctx, a.db, func(tx pgx.Tx) error {
		q := db.New(tx)
		row, err := q.GetUserIdentityByFirebaseUID(ctx, tok.UID)
		if errors.Is(err, pgx.ErrNoRows) {
			id, err = q.CreateUser(ctx, db.CreateUserParams{
				FirebaseUid: tok.UID,
				Email:       email,
				DisplayName: DisplayName(*tok),
			})
			if err == nil {
				return nil
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return fmt.Errorf("creating user: %w", err)
			}
			// A concurrent request created the user between the lookup and
			// the insert. ON CONFLICT DO NOTHING waited for it to commit, and
			// in READ COMMITTED this new statement sees its row.
			row, err = q.GetUserIdentityByFirebaseUID(ctx, tok.UID)
		}
		if err != nil {
			return fmt.Errorf("looking up user: %w", err)
		}
		id = row.ID
		if row.Email != email {
			if err := q.UpdateUserEmail(ctx, db.UpdateUserEmailParams{ID: row.ID, Email: email}); err != nil {
				return fmt.Errorf("updating email: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return pgtype.UUID{}, fmt.Errorf("auth: %w", err)
	}
	return id, nil
}

type userIDKey struct{}

// WithUserID returns a copy of ctx carrying the signed-in user's ID. The
// server's auth middleware calls it; tests of handlers can too.
func WithUserID(ctx context.Context, id pgtype.UUID) context.Context {
	return context.WithValue(ctx, userIDKey{}, id)
}

// UserID returns the signed-in user's ID stored by WithUserID. ok is false if
// there is none, which for an endpoint behind the auth middleware is a bug.
func UserID(ctx context.Context) (id pgtype.UUID, ok bool) {
	id, ok = ctx.Value(userIDKey{}).(pgtype.UUID)
	return id, ok && id.Valid
}

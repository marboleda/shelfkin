package auth_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/marboleda/shelfkin/api/db/dbtest"
	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/auth/authtest"
)

func TestDisplayName(t *testing.T) {
	long := strings.Repeat("é", 60)
	tests := []struct {
		name  string
		token auth.Token
		want  string
	}{
		{"name claim", auth.Token{UID: "u", Name: "Sam Lee", Email: "sam@example.com"}, "Sam Lee"},
		{"name is trimmed", auth.Token{UID: "u", Name: "  Sam  ", Email: "sam@example.com"}, "Sam"},
		{"email local part when no name", auth.Token{UID: "u", Email: "sam.lee@example.com"}, "sam.lee"},
		{"email local part when name is blank", auth.Token{UID: "u", Name: "   ", Email: "sam@example.com"}, "sam"},
		{"last @ splits a quoted local part", auth.Token{UID: "u", Email: `"a@b"@example.com`}, `"a@b"`},
		{"default when no name or email", auth.Token{UID: "u"}, auth.DefaultDisplayName},
		{"default when email has an empty local part", auth.Token{UID: "u", Email: "@example.com"}, auth.DefaultDisplayName},
		{"default when email has no @", auth.Token{UID: "u", Email: "not-an-email"}, auth.DefaultDisplayName},
		{"long name cut to 50 characters", auth.Token{UID: "u", Name: long}, strings.Repeat("é", 50)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := auth.DisplayName(tt.token); got != tt.want {
				t.Errorf("DisplayName(%+v) = %q, want %q", tt.token, got, tt.want)
			}
		})
	}
	if auth.DefaultDisplayName != "Shelfkin user" {
		t.Errorf("DefaultDisplayName = %q, want %q", auth.DefaultDisplayName, "Shelfkin user")
	}
}

func TestUserIDContext(t *testing.T) {
	if _, ok := auth.UserID(context.Background()); ok {
		t.Error("UserID found a user in an empty context")
	}
	if _, ok := auth.UserID(auth.WithUserID(context.Background(), pgtype.UUID{})); ok {
		t.Error("UserID accepted an invalid (unset) UUID")
	}
	id := pgtype.UUID{Bytes: [16]byte{1, 2, 3}, Valid: true}
	got, ok := auth.UserID(auth.WithUserID(context.Background(), id))
	if !ok || got != id {
		t.Errorf("UserID = %v, %v; want %v, true", got, ok, id)
	}
}

type userRow struct {
	ID          pgtype.UUID
	DisplayName string
	Email       pgtype.Text
	Timezone    string
	PushEnabled bool
	CreatedAt   pgtype.Timestamptz
	UpdatedAt   pgtype.Timestamptz
}

// usersFor returns every users row for firebaseUID, read as the pool's
// (superuser) connection.
func usersFor(t *testing.T, pool *pgxpool.Pool, firebaseUID string) []userRow {
	t.Helper()
	rows, err := pool.Query(context.Background(),
		`SELECT id, display_name, email, timezone, push_enabled, created_at, updated_at
		 FROM users WHERE firebase_uid = $1`, firebaseUID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []userRow
	for rows.Next() {
		var r userRow
		if err := rows.Scan(&r.ID, &r.DisplayName, &r.Email, &r.Timezone, &r.PushEnabled, &r.CreatedAt, &r.UpdatedAt); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func onlyUser(t *testing.T, pool *pgxpool.Pool, firebaseUID string) userRow {
	t.Helper()
	rows := usersFor(t, pool, firebaseUID)
	if len(rows) != 1 {
		t.Fatalf("got %d users rows for %s, want 1", len(rows), firebaseUID)
	}
	return rows[0]
}

func TestAuthenticateCreatesUserOnFirstSight(t *testing.T) {
	pool := dbtest.New(t)
	v := authtest.NewVerifier()
	a := auth.NewAuthenticator(v, pool)
	ctx := context.Background()

	tests := []struct {
		name      string
		token     auth.Token
		wantName  string
		wantEmail pgtype.Text
	}{
		{"name and email", auth.Token{UID: "uid-1", Email: "sam@example.com", Name: "Sam"}, "Sam", pgtype.Text{String: "sam@example.com", Valid: true}},
		{"email only", auth.Token{UID: "uid-2", Email: "alex@example.com"}, "alex", pgtype.Text{String: "alex@example.com", Valid: true}},
		{"neither", auth.Token{UID: "uid-3"}, "Shelfkin user", pgtype.Text{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := a.Authenticate(ctx, v.Issue(tt.token))
			if err != nil {
				t.Fatalf("Authenticate: %v", err)
			}
			u := onlyUser(t, pool, tt.token.UID)
			if u.ID != id {
				t.Errorf("returned ID %v, row ID %v", id, u.ID)
			}
			if u.DisplayName != tt.wantName || u.Email != tt.wantEmail || u.Timezone != "UTC" || u.PushEnabled {
				t.Errorf("row = %+v, want display name %q, email %v, timezone UTC, push off", u, tt.wantName, tt.wantEmail)
			}

			// A second request finds the same row and creates no other.
			again, err := a.Authenticate(ctx, v.Issue(tt.token))
			if err != nil {
				t.Fatalf("second Authenticate: %v", err)
			}
			if again != id {
				t.Errorf("second Authenticate = %v, want %v", again, id)
			}
			if u2 := onlyUser(t, pool, tt.token.UID); u2.UpdatedAt != u.UpdatedAt {
				t.Errorf("an unchanged token updated the row: updated_at %v -> %v", u.UpdatedAt, u2.UpdatedAt)
			}
		})
	}
}

func TestAuthenticateConcurrentFirstRequests(t *testing.T) {
	pool := dbtest.New(t)
	v := authtest.NewVerifier()
	a := auth.NewAuthenticator(v, pool)

	// Several rounds, each with many requests released at once, to make the
	// lookup-then-insert race likely.
	const n = 16
	for round := range 5 {
		uid := "racer-" + string(rune('a'+round))
		token := v.Issue(auth.Token{UID: uid, Email: uid + "@example.com"})
		var wg sync.WaitGroup
		start := make(chan struct{})
		ids := make([]pgtype.UUID, n)
		errs := make([]error, n)
		for i := range n {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				ids[i], errs[i] = a.Authenticate(context.Background(), token)
			}()
		}
		close(start)
		wg.Wait()
		for i := range n {
			if errs[i] != nil {
				t.Fatalf("round %d request %d: %v", round, i, errs[i])
			}
			if ids[i] != ids[0] {
				t.Errorf("round %d request %d got user %v, request 0 got %v", round, i, ids[i], ids[0])
			}
		}
		onlyUser(t, pool, uid)
	}
}

func TestAuthenticateUpdatesEmailButNotDisplayName(t *testing.T) {
	pool := dbtest.New(t)
	v := authtest.NewVerifier()
	a := auth.NewAuthenticator(v, pool)
	ctx := context.Background()

	id, err := a.Authenticate(ctx, v.Issue(auth.Token{UID: "u", Email: "old@example.com", Name: "Original"}))
	if err != nil {
		t.Fatal(err)
	}
	before := onlyUser(t, pool, "u")

	// A new email and a new name in the token: only the email changes.
	got, err := a.Authenticate(ctx, v.Issue(auth.Token{UID: "u", Email: "new@example.com", Name: "Renamed"}))
	if err != nil {
		t.Fatal(err)
	}
	if got != id {
		t.Fatalf("Authenticate = %v, want %v", got, id)
	}
	after := onlyUser(t, pool, "u")
	if after.Email.String != "new@example.com" || !after.Email.Valid {
		t.Errorf("email = %v, want new@example.com", after.Email)
	}
	if after.DisplayName != "Original" {
		t.Errorf("display name = %q, want it kept as Original", after.DisplayName)
	}
	if !after.UpdatedAt.Time.After(before.UpdatedAt.Time) {
		t.Errorf("updated_at %v not after %v", after.UpdatedAt.Time, before.UpdatedAt.Time)
	}

	// A token without an email clears it: the stored email follows Firebase.
	if _, err := a.Authenticate(ctx, v.Issue(auth.Token{UID: "u"})); err != nil {
		t.Fatal(err)
	}
	if e := onlyUser(t, pool, "u").Email; e.Valid {
		t.Errorf("email = %v, want NULL after a token with no email", e)
	}
}

type errVerifier struct{ err error }

func (v errVerifier) Verify(context.Context, string) (*auth.Token, error) { return nil, v.err }

func TestAuthenticateRejectsInvalidTokens(t *testing.T) {
	pool := dbtest.New(t)
	v := authtest.NewVerifier()
	a := auth.NewAuthenticator(v, pool)

	_, err := a.Authenticate(context.Background(), "never-issued")
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("unknown token: err = %v, want ErrInvalidToken", err)
	}

	// A verifier failure that is not about the token (say, Google's keys
	// could not be fetched) must not look like a bad token.
	boom := errors.New("keys unavailable")
	_, err = auth.NewAuthenticator(errVerifier{boom}, pool).Authenticate(context.Background(), "t")
	if !errors.Is(err, boom) || errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("verifier failure: err = %v, want it wrapped and not ErrInvalidToken", err)
	}

	var count int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM users").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Errorf("rejected tokens created %d users", count)
	}
}

// TestAuthenticateRunsAsAppRole checks the first-sight lookup goes through
// the restricted role: if shelfkin_app could not write users, it fails.
func TestAuthenticateRunsAsAppRole(t *testing.T) {
	pool := dbtest.New(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, "REVOKE INSERT ON users FROM shelfkin_app"); err != nil {
		t.Fatal(err)
	}
	v := authtest.NewVerifier()
	_, err := auth.NewAuthenticator(v, pool).Authenticate(ctx, v.Issue(auth.Token{UID: "u"}))
	if err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Errorf("err = %v, want permission denied from running as shelfkin_app", err)
	}
}

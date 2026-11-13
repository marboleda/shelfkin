package main

import (
	"context"
	"crypto/hmac"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// APIURLEnv names the environment variable holding the DATABASE_URL the API
// connects with (the api-database-url secret in production). When it is set,
// `migrate up` creates the API login role named by API_DB_ROLE if it is
// missing, keeps its password in step with the URL, and, after the grant
// step, checks that the URL logs in as that role (#92).
const APIURLEnv = "API_DATABASE_URL"

// movingSection is the README section that explains how production's
// shelfkin_api moves off the Cloud SQL built-in user (#92).
const movingSection = "README, \"Deploying the API\" > \"Moving `shelfkin_api` off the Cloud SQL built-in user\""

// scramIterations is Postgres's default scram_iterations.
const scramIterations = 4096

// apiLogin is a parsed API_DATABASE_URL. The password is only ever used to
// log in and to compute a SCRAM verifier; it is never printed or sent in SQL.
type apiLogin struct {
	cfg *pgx.ConnConfig
	// secrets are the strings that must never appear in output or errors: the
	// password and the full URL.
	secrets []string
}

// parseAPILogin checks API_DATABASE_URL against API_DB_ROLE before anything
// is created or changed. Its errors name the wrong variable and never contain
// the URL or the password.
func parseAPILogin(apiURL, apiRole string) (*apiLogin, error) {
	if apiRole == "" {
		return nil, fmt.Errorf("%s is set but %s is not; set %s to the user name in %s", APIURLEnv, APIRoleEnv, APIRoleEnv, APIURLEnv)
	}
	if apiRole == appRole {
		return nil, fmt.Errorf("%s must be the login role the API connects as, not %s", APIRoleEnv, appRole)
	}
	cfg, err := pgx.ParseConfig(apiURL)
	if err != nil {
		// The parse error can quote parts of the URL, so it is dropped.
		return nil, fmt.Errorf("%s is not a valid connection string", APIURLEnv)
	}
	if cfg.User != apiRole {
		return nil, fmt.Errorf("%s=%s does not match the user name in %s (%s)", APIRoleEnv, apiRole, APIURLEnv, cfg.User)
	}
	if cfg.Password == "" {
		return nil, fmt.Errorf("%s has no password", APIURLEnv)
	}
	for _, r := range cfg.Password {
		// Postgres runs SASLprep on non-ASCII passwords before hashing them, so
		// only printable ASCII is hashed here exactly as the server would.
		if r < 0x20 || r > 0x7e {
			return nil, fmt.Errorf("the password in %s must be printable ASCII", APIURLEnv)
		}
	}
	return &apiLogin{cfg: cfg, secrets: []string{apiURL, cfg.Password}}, nil
}

// where names the login without its password: user, host and database.
func (l *apiLogin) where() string {
	return fmt.Sprintf("user=%s host=%s database=%s", l.cfg.User, l.cfg.Host, l.cfg.Database)
}

// redact returns err's text with every secret replaced. The result does not
// wrap err, so nothing upstream can print the original text.
func redact(err error, secrets ...string) string {
	msg := err.Error()
	for _, s := range secrets {
		if s != "" {
			msg = strings.ReplaceAll(msg, s, "[redacted]")
		}
	}
	return msg
}

// connectAPI logs in with API_DATABASE_URL.
func (l *apiLogin) connect(ctx context.Context) (*pgx.Conn, error) {
	cfg := l.cfg.Copy()
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = 10 * time.Second
	}
	return pgx.ConnectConfig(ctx, cfg)
}

// sqlStateOf returns the SQLSTATE of err, or "".
func sqlStateOf(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// ensureAPILogin creates the API login role if it is missing, or sets its
// password when API_DATABASE_URL cannot log in with it. The password is sent
// only as a SCRAM-SHA-256 verifier, so it never appears in SQL text or in the
// server's log of a failed statement.
//
// When shelfkin_migrator creates the role (Postgres 16+, CREATEROLE, not a
// superuser) it gets ADMIN on it, which lets later runs ALTER its password.
// The new role itself gets no membership; the grant step adds shelfkin_app.
func ensureAPILogin(ctx context.Context, sqlDB *sql.DB, login *apiLogin, out io.Writer) error {
	role := login.cfg.User
	ident := pgx.Identifier{role}.Sanitize()

	var self string
	var exists bool
	if err := sqlDB.QueryRowContext(ctx,
		"SELECT current_user, EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = $1)", role).Scan(&self, &exists); err != nil {
		return fmt.Errorf("looking up role %s: %s", role, redact(err, login.secrets...))
	}
	if self == role {
		return fmt.Errorf("%s=%s is the role migrations run as; it must be a separate login", APIRoleEnv, role)
	}

	if !exists {
		verifier, err := scramVerifier(login.cfg.Password)
		if err != nil {
			return err
		}
		stmt := fmt.Sprintf("CREATE ROLE %s LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOBYPASSRLS NOREPLICATION PASSWORD '%s'", ident, verifier)
		if _, err := sqlDB.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("creating login role %s: %s", role, redact(err, append(login.secrets, verifier)...))
		}
		fmt.Fprintf(out, "Created login role %s\n", role)
		return nil
	}

	conn, err := login.connect(ctx)
	if err == nil {
		conn.Close(ctx)
		fmt.Fprintf(out, "%s can log in; password unchanged\n", role)
		return nil
	}
	if sqlStateOf(err) != "28P01" {
		return fmt.Errorf("role %s exists, but logging in with %s (%s) failed: %s",
			role, APIURLEnv, login.where(), redact(err, login.secrets...))
	}

	verifier, err := scramVerifier(login.cfg.Password)
	if err != nil {
		return err
	}
	if _, err := sqlDB.ExecContext(ctx, fmt.Sprintf("ALTER ROLE %s PASSWORD '%s'", ident, verifier)); err != nil {
		return fmt.Errorf("role %s exists and %s cannot log in, but setting its password failed: %s.\n"+
			"The role running migrations can only change the password of a role it has ADMIN on, such as one it created. "+
			"See %s.",
			role, APIURLEnv, redact(err, append(login.secrets, verifier)...), movingSection)
	}
	fmt.Fprintf(out, "Set a new password for %s\n", role)
	return nil
}

// verifyAPILogin connects with API_DATABASE_URL and checks that it logs in as
// apiRole, so the API is only deployed once it is known to be able to connect.
func verifyAPILogin(ctx context.Context, login *apiLogin, apiRole string, out io.Writer) error {
	conn, err := login.connect(ctx)
	if err != nil {
		return fmt.Errorf("logging in with %s (%s) after the grant step failed: %s",
			APIURLEnv, login.where(), redact(err, login.secrets...))
	}
	defer conn.Close(ctx)
	var user string
	if err := conn.QueryRow(ctx, "SELECT current_user").Scan(&user); err != nil {
		return fmt.Errorf("checking the login with %s (%s): %s", APIURLEnv, login.where(), redact(err, login.secrets...))
	}
	if user != apiRole {
		return fmt.Errorf("%s logs in as %s, want %s=%s", APIURLEnv, user, APIRoleEnv, apiRole)
	}
	fmt.Fprintf(out, "Checked: %s can log in\n", apiRole)
	return nil
}

// verifierPattern is the only shape a verifier may have before it is put in
// a quoted SQL literal: digits, base64 characters, '$' and ':'.
var verifierPattern = regexp.MustCompile(`^SCRAM-SHA-256\$[0-9]+:[A-Za-z0-9+/]+={0,2}\$[A-Za-z0-9+/]+={0,2}:[A-Za-z0-9+/]+={0,2}$`)

// scramVerifier computes a SCRAM-SHA-256 verifier for password with a random
// 16-byte salt, in the form Postgres stores in pg_authid.rolpassword:
// SCRAM-SHA-256$<iterations>:<salt>$<StoredKey>:<ServerKey> (RFC 5802,
// RFC 7677). Postgres stores a password given in this form as it is.
func scramVerifier(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generating a salt: %w", err)
	}
	return scramVerifierWithSalt(password, salt, scramIterations)
}

func scramVerifierWithSalt(password string, salt []byte, iterations int) (string, error) {
	salted, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		return "", errors.New("computing the SCRAM verifier failed")
	}
	clientKey := hmacSHA256(salted, "Client Key")
	storedKey := sha256.Sum256(clientKey)
	serverKey := hmacSHA256(salted, "Server Key")
	b64 := base64.StdEncoding.EncodeToString
	v := fmt.Sprintf("SCRAM-SHA-256$%d:%s$%s:%s", iterations, b64(salt), b64(storedKey[:]), b64(serverKey))
	if !verifierPattern.MatchString(v) {
		return "", errors.New("the SCRAM verifier has an unexpected format")
	}
	return v, nil
}

func hmacSHA256(key []byte, msg string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(msg))
	return h.Sum(nil)
}

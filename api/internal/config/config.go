// Package config reads the API server's configuration from environment
// variables.
package config

import (
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Environment variable names.
const (
	DatabaseURLEnv = "DATABASE_URL"
	PortEnv        = "PORT"
	LogLevelEnv    = "LOG_LEVEL"
	// CORSAllowedOriginsEnv is a comma-separated list of browser origins
	// (scheme://host[:port]) allowed to call the API.
	CORSAllowedOriginsEnv = "CORS_ALLOWED_ORIGINS"
	// FirebaseProjectIDEnv is the Firebase project whose ID tokens the API
	// accepts.
	FirebaseProjectIDEnv = "FIREBASE_PROJECT_ID"
	// AuthEmulatorHostEnv (host:port) makes the API accept unsigned tokens
	// from the Firebase Auth emulator. Never set it in production.
	AuthEmulatorHostEnv = "FIREBASE_AUTH_EMULATOR_HOST"
)

// Defaults for the optional variables.
const (
	DefaultPort     = 8080
	DefaultLogLevel = slog.LevelInfo
)

// DefaultCORSAllowedOrigins is used when CORS_ALLOWED_ORIGINS is unset: the
// origin of the local Expo web dev server (npx expo start --web).
var DefaultCORSAllowedOrigins = []string{"http://localhost:8081"}

// Config is the API server's configuration.
type Config struct {
	// Pool is the parsed DATABASE_URL. Its password is in
	// Pool.ConnConfig.Password; never log the config or the URL.
	Pool     *pgxpool.Config
	Port     int
	LogLevel slog.Level
	// CORSAllowedOrigins are the normalized origins (lowercase, no trailing
	// slash) allowed to call the API from a browser. Never empty and never
	// "*".
	CORSAllowedOrigins []string
	// FirebaseProjectID is the project ID tokens must be issued for.
	FirebaseProjectID string
	// AuthEmulatorHost is FIREBASE_AUTH_EMULATOR_HOST, or "" when tokens
	// must come from real Firebase.
	AuthEmulatorHost string
}

// Error reports one or more invalid environment variables. Its message names
// each bad variable and never contains the database password.
type Error struct {
	// Problems holds one message per bad variable, in a fixed order.
	Problems []string
}

func (e *Error) Error() string {
	return "invalid configuration: " + strings.Join(e.Problems, "; ")
}

var logLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// LoadAPI reads the API server's configuration: everything Load reads, with
// FIREBASE_PROJECT_ID required. It returns an *Error naming every invalid
// variable.
func LoadAPI(getenv func(string) string) (*Config, error) {
	return load(getenv, true)
}

// Load reads the configuration using getenv (os.Getenv in production). An
// empty variable counts as unset. FIREBASE_PROJECT_ID is optional here
// (programs other than the API server, such as the expiry job, do not verify
// tokens); the API server uses LoadAPI. It returns an *Error naming every
// invalid variable.
func Load(getenv func(string) string) (*Config, error) {
	return load(getenv, false)
}

func load(getenv func(string) string, requireFirebase bool) (*Config, error) {
	cfg := &Config{
		Port:               DefaultPort,
		LogLevel:           DefaultLogLevel,
		CORSAllowedOrigins: append([]string(nil), DefaultCORSAllowedOrigins...),
	}
	var problems []string

	if raw := getenv(DatabaseURLEnv); raw == "" {
		problems = append(problems, DatabaseURLEnv+" is required")
	} else if pool, err := pgxpool.ParseConfig(raw); err != nil {
		// The parse error is deliberately not included: it can quote the URL,
		// and with it the password.
		problems = append(problems, DatabaseURLEnv+" is not a valid Postgres connection string")
	} else {
		cfg.Pool = pool
	}

	cfg.FirebaseProjectID = getenv(FirebaseProjectIDEnv)
	if requireFirebase && cfg.FirebaseProjectID == "" {
		problems = append(problems, FirebaseProjectIDEnv+" is required")
	}
	cfg.AuthEmulatorHost = getenv(AuthEmulatorHostEnv)

	if raw := getenv(PortEnv); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			problems = append(problems, fmt.Sprintf("%s must be a port number from 1 to 65535, got %q", PortEnv, raw))
		} else {
			cfg.Port = port
		}
	}

	if raw := getenv(LogLevelEnv); raw != "" {
		level, ok := logLevels[strings.ToLower(raw)]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s must be one of debug, info, warn or error, got %q", LogLevelEnv, raw))
		} else {
			cfg.LogLevel = level
		}
	}

	if raw := getenv(CORSAllowedOriginsEnv); raw != "" {
		origins, err := parseOrigins(raw)
		if err != nil {
			problems = append(problems, fmt.Sprintf("%s %v", CORSAllowedOriginsEnv, err))
		} else {
			cfg.CORSAllowedOrigins = origins
		}
	}

	if len(problems) > 0 {
		return nil, &Error{Problems: problems}
	}
	return cfg, nil
}

// parseOrigins parses a comma-separated list of origins. Blank entries are
// skipped; at least one origin is required. Each origin must be
// http(s)://host[:port] with no path, query or credentials, and "*" is
// rejected because requests carry bearer tokens. Origins are lowercased and
// a trailing slash is dropped, matching the Origin header browsers send.
func parseOrigins(raw string) ([]string, error) {
	var origins []string
	seen := map[string]bool{}
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "*") {
			return nil, fmt.Errorf("must not contain a wildcard, got %q", entry)
		}
		u, err := url.Parse(entry)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
			u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" ||
			strings.HasSuffix(entry, "?") || strings.HasSuffix(entry, "#") {
			return nil, fmt.Errorf("must list origins like https://example.com or http://localhost:8081, got %q", entry)
		}
		origin := strings.ToLower(u.Scheme + "://" + u.Host)
		if !seen[origin] {
			seen[origin] = true
			origins = append(origins, origin)
		}
	}
	if len(origins) == 0 {
		return nil, fmt.Errorf("must list at least one origin")
	}
	return origins, nil
}

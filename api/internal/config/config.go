// Package config reads the API server's configuration from environment
// variables.
package config

import (
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Environment variable names.
const (
	DatabaseURLEnv = "DATABASE_URL"
	PortEnv        = "PORT"
	LogLevelEnv    = "LOG_LEVEL"
)

// Defaults for the optional variables.
const (
	DefaultPort     = 8080
	DefaultLogLevel = slog.LevelInfo
)

// Config is the API server's configuration.
type Config struct {
	// Pool is the parsed DATABASE_URL. Its password is in
	// Pool.ConnConfig.Password; never log the config or the URL.
	Pool     *pgxpool.Config
	Port     int
	LogLevel slog.Level
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

// Load reads the configuration using getenv (os.Getenv in production). An
// empty variable counts as unset. It returns an *Error naming every invalid
// variable.
func Load(getenv func(string) string) (*Config, error) {
	cfg := &Config{Port: DefaultPort, LogLevel: DefaultLogLevel}
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

	if len(problems) > 0 {
		return nil, &Error{Problems: problems}
	}
	return cfg, nil
}

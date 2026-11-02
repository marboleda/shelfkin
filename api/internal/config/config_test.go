package config

import (
	"errors"
	"log/slog"
	"strings"
	"testing"
)

const (
	secret   = "s3cr3t-Pa55word"
	validURL = "postgres://shelfkin:" + secret + "@localhost:5432/shelfkin?sslmode=disable"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{DatabaseURLEnv: validURL}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
	}
	if cfg.Pool == nil || cfg.Pool.ConnConfig.Host != "localhost" || cfg.Pool.ConnConfig.Database != "shelfkin" {
		t.Errorf("Pool config not parsed from DATABASE_URL: %+v", cfg.Pool)
	}
}

func TestLoadValid(t *testing.T) {
	tests := []struct {
		name      string
		vars      map[string]string
		wantPort  int
		wantLevel slog.Level
	}{
		{"port", map[string]string{PortEnv: "9090"}, 9090, slog.LevelInfo},
		{"debug", map[string]string{LogLevelEnv: "debug"}, 8080, slog.LevelDebug},
		{"info", map[string]string{LogLevelEnv: "info"}, 8080, slog.LevelInfo},
		{"warn", map[string]string{LogLevelEnv: "warn"}, 8080, slog.LevelWarn},
		{"error", map[string]string{LogLevelEnv: "error"}, 8080, slog.LevelError},
		{"level is case-insensitive", map[string]string{LogLevelEnv: "WARN"}, 8080, slog.LevelWarn},
		{"keyword/value DATABASE_URL", map[string]string{DatabaseURLEnv: "host=localhost user=shelfkin password=" + secret + " dbname=shelfkin"}, 8080, slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vars := map[string]string{DatabaseURLEnv: validURL}
			for k, v := range tt.vars {
				vars[k] = v
			}
			cfg, err := Load(env(vars))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Port != tt.wantPort || cfg.LogLevel != tt.wantLevel {
				t.Errorf("got port %d level %v, want port %d level %v", cfg.Port, cfg.LogLevel, tt.wantPort, tt.wantLevel)
			}
		})
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		name    string
		vars    map[string]string
		wantVar string
	}{
		{"missing DATABASE_URL", map[string]string{}, DatabaseURLEnv},
		{"unparseable DATABASE_URL: bad port", map[string]string{DatabaseURLEnv: "postgres://shelfkin:" + secret + "@localhost:notaport/shelfkin"}, DatabaseURLEnv},
		{"unparseable DATABASE_URL: bad scheme-less URL", map[string]string{DatabaseURLEnv: "postgres://shelfkin:" + secret + "@[::1/shelfkin"}, DatabaseURLEnv},
		{"unparseable DATABASE_URL: bad sslmode", map[string]string{DatabaseURLEnv: validURL + "&sslmode=bogus-" + secret}, DatabaseURLEnv},
		{"unparseable DATABASE_URL: bad keyword/value", map[string]string{DatabaseURLEnv: "password=" + secret + " host='unterminated"}, DatabaseURLEnv},
		{"non-numeric PORT", map[string]string{DatabaseURLEnv: validURL, PortEnv: "http"}, PortEnv},
		{"PORT out of range", map[string]string{DatabaseURLEnv: validURL, PortEnv: "70000"}, PortEnv},
		{"PORT zero", map[string]string{DatabaseURLEnv: validURL, PortEnv: "0"}, PortEnv},
		{"unknown LOG_LEVEL", map[string]string{DatabaseURLEnv: validURL, LogLevelEnv: "verbose"}, LogLevelEnv},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(env(tt.vars))
			if err == nil {
				t.Fatalf("Load succeeded with %+v, want error", cfg)
			}
			var cerr *Error
			if !errors.As(err, &cerr) {
				t.Fatalf("error is %T, want *config.Error", err)
			}
			msg := err.Error()
			if !strings.Contains(msg, tt.wantVar) {
				t.Errorf("error %q does not name %s", msg, tt.wantVar)
			}
			if strings.Contains(msg, secret) {
				t.Errorf("error %q contains the database password", msg)
			}
			if strings.Contains(msg, "\n") {
				t.Errorf("error %q spans more than one line", msg)
			}
		})
	}
}

func TestLoadNamesEveryBadVariable(t *testing.T) {
	_, err := Load(env(map[string]string{PortEnv: "x", LogLevelEnv: "loud"}))
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	for _, v := range []string{DatabaseURLEnv, PortEnv, LogLevelEnv} {
		if !strings.Contains(err.Error(), v) {
			t.Errorf("error %q does not name %s", err, v)
		}
	}
}

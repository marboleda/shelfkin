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

const testProject = "demo-test"

// env returns a getenv for vars. FIREBASE_PROJECT_ID is testProject unless
// vars sets it (to "" to leave it unset).
func env(vars map[string]string) func(string) string {
	return func(k string) string {
		if v, ok := vars[k]; ok {
			return v
		}
		if k == FirebaseProjectIDEnv {
			return testProject
		}
		return ""
	}
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
	if cfg.FirebaseProjectID != testProject {
		t.Errorf("FirebaseProjectID = %q, want %q", cfg.FirebaseProjectID, testProject)
	}
	if cfg.AuthEmulatorHost != "" {
		t.Errorf("AuthEmulatorHost = %q, want empty when unset", cfg.AuthEmulatorHost)
	}
}

func TestLoadFirebase(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		DatabaseURLEnv:       validURL,
		FirebaseProjectIDEnv: "demo-shelfkin",
		AuthEmulatorHostEnv:  "localhost:9099",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.FirebaseProjectID != "demo-shelfkin" || cfg.AuthEmulatorHost != "localhost:9099" {
		t.Errorf("got project %q emulator %q, want demo-shelfkin and localhost:9099", cfg.FirebaseProjectID, cfg.AuthEmulatorHost)
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
		{"missing FIREBASE_PROJECT_ID", map[string]string{DatabaseURLEnv: validURL, FirebaseProjectIDEnv: ""}, FirebaseProjectIDEnv},
		{"unparseable DATABASE_URL: bad port", map[string]string{DatabaseURLEnv: "postgres://shelfkin:" + secret + "@localhost:notaport/shelfkin"}, DatabaseURLEnv},
		{"unparseable DATABASE_URL: bad scheme-less URL", map[string]string{DatabaseURLEnv: "postgres://shelfkin:" + secret + "@[::1/shelfkin"}, DatabaseURLEnv},
		{"unparseable DATABASE_URL: bad sslmode", map[string]string{DatabaseURLEnv: validURL + "&sslmode=bogus-" + secret}, DatabaseURLEnv},
		{"unparseable DATABASE_URL: bad keyword/value", map[string]string{DatabaseURLEnv: "password=" + secret + " host='unterminated"}, DatabaseURLEnv},
		{"non-numeric PORT", map[string]string{DatabaseURLEnv: validURL, PortEnv: "http"}, PortEnv},
		{"PORT out of range", map[string]string{DatabaseURLEnv: validURL, PortEnv: "70000"}, PortEnv},
		{"PORT zero", map[string]string{DatabaseURLEnv: validURL, PortEnv: "0"}, PortEnv},
		{"unknown LOG_LEVEL", map[string]string{DatabaseURLEnv: validURL, LogLevelEnv: "verbose"}, LogLevelEnv},
		{"CORS wildcard", map[string]string{DatabaseURLEnv: validURL, CORSAllowedOriginsEnv: "*"}, CORSAllowedOriginsEnv},
		{"CORS wildcard among origins", map[string]string{DatabaseURLEnv: validURL, CORSAllowedOriginsEnv: "https://shelfkin.web.app,*"}, CORSAllowedOriginsEnv},
		{"CORS wildcard subdomain", map[string]string{DatabaseURLEnv: validURL, CORSAllowedOriginsEnv: "https://*.web.app"}, CORSAllowedOriginsEnv},
		{"CORS no scheme", map[string]string{DatabaseURLEnv: validURL, CORSAllowedOriginsEnv: "shelfkin.web.app"}, CORSAllowedOriginsEnv},
		{"CORS bad scheme", map[string]string{DatabaseURLEnv: validURL, CORSAllowedOriginsEnv: "ftp://shelfkin.web.app"}, CORSAllowedOriginsEnv},
		{"CORS path", map[string]string{DatabaseURLEnv: validURL, CORSAllowedOriginsEnv: "https://shelfkin.web.app/app"}, CORSAllowedOriginsEnv},
		{"CORS query", map[string]string{DatabaseURLEnv: validURL, CORSAllowedOriginsEnv: "https://shelfkin.web.app?x=1"}, CORSAllowedOriginsEnv},
		{"CORS credentials", map[string]string{DatabaseURLEnv: validURL, CORSAllowedOriginsEnv: "https://user@shelfkin.web.app"}, CORSAllowedOriginsEnv},
		{"CORS only commas", map[string]string{DatabaseURLEnv: validURL, CORSAllowedOriginsEnv: " , ,"}, CORSAllowedOriginsEnv},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := LoadAPI(env(tt.vars))
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
	_, err := LoadAPI(env(map[string]string{PortEnv: "x", LogLevelEnv: "loud", FirebaseProjectIDEnv: ""}))
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	for _, v := range []string{DatabaseURLEnv, FirebaseProjectIDEnv, PortEnv, LogLevelEnv} {
		if !strings.Contains(err.Error(), v) {
			t.Errorf("error %q does not name %s", err, v)
		}
	}
}

func TestLoadCORSAllowedOrigins(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"unset uses the Expo web dev server", "", []string{"http://localhost:8081"}},
		{"one origin", "https://shelfkin.web.app", []string{"https://shelfkin.web.app"}},
		{
			"comma-separated with spaces and blanks",
			" https://shelfkin.web.app , ,https://shelfkin.firebaseapp.com,",
			[]string{"https://shelfkin.web.app", "https://shelfkin.firebaseapp.com"},
		},
		{"port kept", "http://localhost:19006", []string{"http://localhost:19006"}},
		{"lowercased, trailing slash dropped", "HTTPS://Shelfkin.Web.App/", []string{"https://shelfkin.web.app"}},
		{"duplicates removed", "https://a.example,https://a.example/", []string{"https://a.example"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(env(map[string]string{DatabaseURLEnv: validURL, CORSAllowedOriginsEnv: tt.raw}))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if strings.Join(cfg.CORSAllowedOrigins, " ") != strings.Join(tt.want, " ") {
				t.Errorf("CORSAllowedOrigins = %q, want %q", cfg.CORSAllowedOrigins, tt.want)
			}
		})
	}
}

func TestDefaultCORSOriginsNotShared(t *testing.T) {
	cfg, err := Load(env(map[string]string{DatabaseURLEnv: validURL}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg.CORSAllowedOrigins[0] = "changed"
	if DefaultCORSAllowedOrigins[0] != "http://localhost:8081" {
		t.Errorf("changing a Config changed DefaultCORSAllowedOrigins to %q", DefaultCORSAllowedOrigins)
	}
}

// TestLoadFirebaseProjectOptional: only the API server (LoadAPI) needs
// FIREBASE_PROJECT_ID; other programs sharing Load, such as the expiry job,
// do not verify tokens.
func TestLoadFirebaseProjectOptional(t *testing.T) {
	vars := map[string]string{DatabaseURLEnv: validURL, FirebaseProjectIDEnv: ""}
	cfg, err := Load(env(vars))
	if err != nil {
		t.Fatalf("Load without FIREBASE_PROJECT_ID: %v", err)
	}
	if cfg.FirebaseProjectID != "" {
		t.Errorf("FirebaseProjectID = %q, want empty", cfg.FirebaseProjectID)
	}
	if _, err := LoadAPI(env(vars)); err == nil || !strings.Contains(err.Error(), FirebaseProjectIDEnv) {
		t.Errorf("LoadAPI without FIREBASE_PROJECT_ID: err = %v, want it to name %s", err, FirebaseProjectIDEnv)
	}
}

func TestLoadAppBaseURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"unset uses the Expo web dev server", "", "http://localhost:8081"},
		{"https", "https://x.web.app", "https://x.web.app"},
		{"trailing slash ignored", "https://x.web.app/", "https://x.web.app"},
		{"port kept", "http://localhost:19006", "http://localhost:19006"},
		{"path kept, trailing slash ignored", "https://example.com/shelfkin/", "https://example.com/shelfkin"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Load(env(map[string]string{DatabaseURLEnv: validURL, AppBaseURLEnv: tt.raw}))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.AppBaseURL != tt.want {
				t.Errorf("AppBaseURL = %q, want %q", cfg.AppBaseURL, tt.want)
			}
		})
	}
}

func TestLoadAppBaseURLInvalid(t *testing.T) {
	for _, raw := range []string{
		"not a url",
		"ftp://x",
		"https://x/?a=1",
		"https://x/#top",
		"https://x?",
		"x.web.app",
		"/join",
		"https://",
		"mailto:sam@example.com",
	} {
		t.Run(raw, func(t *testing.T) {
			cfg, err := LoadAPI(env(map[string]string{DatabaseURLEnv: validURL, AppBaseURLEnv: raw}))
			if err == nil {
				t.Fatalf("Load succeeded with AppBaseURL %q, want error", cfg.AppBaseURL)
			}
			var cerr *Error
			if !errors.As(err, &cerr) {
				t.Fatalf("error is %T, want *config.Error", err)
			}
			if !strings.Contains(err.Error(), AppBaseURLEnv) {
				t.Errorf("error %q does not name %s", err, AppBaseURLEnv)
			}
		})
	}
}

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

const password = "main-test-pw-42"

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestRunConfigErrors(t *testing.T) {
	goodURL := "postgres://shelfkin:" + password + "@localhost:5432/shelfkin"
	tests := []struct {
		name    string
		vars    map[string]string
		wantVar string
	}{
		{"missing DATABASE_URL", map[string]string{"FIREBASE_PROJECT_ID": "demo-test"}, "DATABASE_URL"},
		{"unparseable DATABASE_URL", map[string]string{"DATABASE_URL": "postgres://shelfkin:" + password + "@localhost:nope/shelfkin", "FIREBASE_PROJECT_ID": "demo-test"}, "DATABASE_URL"},
		{"missing FIREBASE_PROJECT_ID", map[string]string{"DATABASE_URL": goodURL}, "FIREBASE_PROJECT_ID"},
		{"non-numeric PORT", map[string]string{"DATABASE_URL": goodURL, "FIREBASE_PROJECT_ID": "demo-test", "PORT": "eighty"}, "PORT"},
		{"unknown LOG_LEVEL", map[string]string{"DATABASE_URL": goodURL, "FIREBASE_PROJECT_ID": "demo-test", "LOG_LEVEL": "trace"}, "LOG_LEVEL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out syncBuffer
			code := run(context.Background(), env(tt.vars), &out, time.Second)
			if code == 0 {
				t.Fatal("exit code 0, want non-zero")
			}
			text := strings.TrimSpace(out.String())
			if strings.Count(text, "\n") != 0 {
				t.Errorf("want exactly one log line, got %q", text)
			}
			var line map[string]any
			if err := json.Unmarshal([]byte(text), &line); err != nil {
				t.Fatalf("log line is not JSON: %q", text)
			}
			if line["level"] != "ERROR" || !strings.Contains(line["msg"].(string), tt.wantVar) {
				t.Errorf("log line %v does not name %s at ERROR", line, tt.wantVar)
			}
			if strings.Contains(text, password) {
				t.Errorf("log line contains the password: %s", text)
			}
			if strings.Contains(text, "goroutine") || strings.Contains(text, "panic") {
				t.Errorf("log line looks like a panic: %s", text)
			}
		})
	}
}


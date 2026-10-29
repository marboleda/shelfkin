package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strconv"
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
		{"missing DATABASE_URL", map[string]string{}, "DATABASE_URL"},
		{"unparseable DATABASE_URL", map[string]string{"DATABASE_URL": "postgres://shelfkin:" + password + "@localhost:nope/shelfkin"}, "DATABASE_URL"},
		{"non-numeric PORT", map[string]string{"DATABASE_URL": goodURL, "PORT": "eighty"}, "PORT"},
		{"unknown LOG_LEVEL", map[string]string{"DATABASE_URL": goodURL, "LOG_LEVEL": "trace"}, "LOG_LEVEL"},
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

// TestRunStartsWithoutDatabaseAndStopsCleanly runs the whole server against
// an address where no database listens: it must start, report 503 from
// /health, then shut down with exit code 0 when its context is cancelled.
func TestRunStartsWithoutDatabaseAndStopsCleanly(t *testing.T) {
	port := freePort(t)
	dbPort := freePort(t)
	vars := map[string]string{
		"DATABASE_URL": "postgres://shelfkin:" + password + "@127.0.0.1:" + strconv.Itoa(dbPort) + "/shelfkin?sslmode=disable",
		"PORT":         strconv.Itoa(port),
		"LOG_LEVEL":    "debug",
	}
	var out syncBuffer
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exit := make(chan int, 1)
	go func() { exit <- run(ctx, env(vars), &out, 5*time.Second) }()

	url := "http://127.0.0.1:" + strconv.Itoa(port) + "/health"
	var resp *http.Response
	deadline := time.Now().Add(10 * time.Second)
	for {
		var err error
		resp, err = http.Get(url)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v\nlogs:\n%s", err, out.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("/health status = %d, want 503", resp.StatusCode)
	}

	cancel()
	select {
	case code := <-exit:
		if code != 0 {
			t.Errorf("exit code = %d, want 0\nlogs:\n%s", code, out.String())
		}
	case <-time.After(10 * time.Second):
		t.Fatal("run did not return after shutdown")
	}

	logs := out.String()
	if strings.Contains(logs, password) {
		t.Errorf("logs contain the database password:\n%s", logs)
	}
	var sawListening, sawStopped bool
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		switch m["msg"] {
		case "listening":
			sawListening = m["port"] == float64(port)
		case "stopped":
			sawStopped = true
		}
	}
	if !sawListening || !sawStopped {
		t.Errorf("want a listening line with the port and a stopped line, got:\n%s", logs)
	}
}

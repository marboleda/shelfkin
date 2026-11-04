package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startRun runs the whole server with vars plus a free PORT and waits until
// it answers. It returns the base URL, the logs, a function that stops it
// and the channel run's exit code arrives on.
func startRun(t *testing.T, vars map[string]string) (string, *syncBuffer, context.CancelFunc, <-chan int) {
	t.Helper()
	port := freePort(t)
	vars["PORT"] = strconv.Itoa(port)
	out := &syncBuffer{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	exit := make(chan int, 1)
	go func() { exit <- run(ctx, env(vars), out, 5*time.Second) }()

	base := "http://127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			break
		}
		select {
		case code := <-exit:
			t.Fatalf("run exited with %d before serving\nlogs:\n%s", code, out.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("server did not start: %v\nlogs:\n%s", err, out.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	return base, out, cancel, exit
}

// logLines parses every JSON log line.
func logLines(t *testing.T, logs string) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		lines = append(lines, m)
	}
	return lines
}

func isEmulatorWarning(m map[string]any) bool {
	msg, _ := m["msg"].(string)
	return m["level"] == "WARN" && strings.Contains(msg, "emulator")
}

// TestRunStartsWithoutDatabaseAndStopsCleanly runs the whole server against
// an address where no database listens: it must start, report 503 from
// /health, reject GET /me without a token with 401, then shut down with exit
// code 0 when its context is cancelled.
func TestRunStartsWithoutDatabaseAndStopsCleanly(t *testing.T) {
	// The Admin SDK reads this from the process environment.
	t.Setenv("FIREBASE_AUTH_EMULATOR_HOST", "")
	vars := map[string]string{
		"DATABASE_URL":        "postgres://shelfkin:" + password + "@127.0.0.1:" + strconv.Itoa(freePort(t)) + "/shelfkin?sslmode=disable",
		"FIREBASE_PROJECT_ID": "demo-test",
		"LOG_LEVEL":           "debug",
	}
	base, out, cancel, exit := startRun(t, vars)
	port, _ := strconv.Atoi(vars["PORT"])

	for path, want := range map[string]int{"/health": http.StatusServiceUnavailable, "/me": http.StatusUnauthorized} {
		resp, err := http.Get(base + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s: status = %d, want %d", path, resp.StatusCode, want)
		}
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
	for _, m := range logLines(t, logs) {
		switch m["msg"] {
		case "listening":
			sawListening = m["port"] == float64(port)
		case "stopped":
			sawStopped = true
		}
		if isEmulatorWarning(m) {
			t.Errorf("emulator warning logged without FIREBASE_AUTH_EMULATOR_HOST: %v", m)
		}
	}
	if !sawListening || !sawStopped {
		t.Errorf("want a listening line with the port and a stopped line, got:\n%s", logs)
	}
}

// TestRunWarnsAboutAuthEmulator checks the startup warning when
// FIREBASE_AUTH_EMULATOR_HOST is set. Nothing needs to listen there.
func TestRunWarnsAboutAuthEmulator(t *testing.T) {
	const host = "127.0.0.1:1"
	t.Setenv("FIREBASE_AUTH_EMULATOR_HOST", host)
	vars := map[string]string{
		"DATABASE_URL":                "postgres://shelfkin:" + password + "@127.0.0.1:" + strconv.Itoa(freePort(t)) + "/shelfkin?sslmode=disable",
		"FIREBASE_PROJECT_ID":         "demo-test",
		"FIREBASE_AUTH_EMULATOR_HOST": host,
	}
	_, out, cancel, exit := startRun(t, vars)
	cancel()
	<-exit

	var warnings []map[string]any
	for _, m := range logLines(t, out.String()) {
		if isEmulatorWarning(m) {
			warnings = append(warnings, m)
		}
	}
	if len(warnings) != 1 {
		t.Fatalf("got %d startup warnings about the auth emulator, want 1:\n%s", len(warnings), out.String())
	}
	if msg := warnings[0]["msg"].(string); !strings.Contains(msg, "production") || warnings[0]["emulator_host"] != host {
		t.Errorf("warning = %v, want it to say never in production and name %s", warnings[0], host)
	}
}

// TestRunRefusesEmulatorMismatch: the Admin SDK reads the emulator host only
// from the process environment, so a configured value that differs from it
// must stop the server rather than accept tokens it was not told to.
func TestRunRefusesEmulatorMismatch(t *testing.T) {
	t.Setenv("FIREBASE_AUTH_EMULATOR_HOST", "127.0.0.1:1")
	var out syncBuffer
	code := run(context.Background(), env(map[string]string{
		"DATABASE_URL":        "postgres://shelfkin:" + password + "@127.0.0.1:5432/shelfkin",
		"FIREBASE_PROJECT_ID": "demo-test",
	}), &out, time.Second)
	if code == 0 {
		t.Fatalf("exit code 0, want non-zero\nlogs:\n%s", out.String())
	}
}

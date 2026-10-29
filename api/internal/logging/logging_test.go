package logging

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
)

func TestNewWritesJSONAtLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelWarn)
	logger.Info("hidden")
	logger.Warn("shown", "port", 8080)

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1: %q", len(lines), buf.String())
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("line is not JSON: %v", err)
	}
	if got["msg"] != "shown" || got["level"] != "WARN" || got["port"] != float64(8080) {
		t.Errorf("unexpected log line %v", got)
	}
}

func TestNewRedactsSecrets(t *testing.T) {
	const secret = "hunter2-pw"
	var buf bytes.Buffer
	logger := New(&buf, slog.LevelInfo, secret, "")
	logger.Error("connecting with "+secret,
		"error", errors.New("auth failed for password "+secret),
		"url", "postgres://u:"+secret+"@h/db",
		slog.Group("g", "inner", secret),
	)
	out := buf.String()
	if strings.Contains(out, secret) {
		t.Fatalf("log contains the secret: %s", out)
	}
	if n := strings.Count(out, Redacted); n != 4 {
		t.Errorf("got %d redactions, want 4: %s", n, out)
	}
}

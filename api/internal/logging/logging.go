// Package logging builds the API server's structured JSON logger.
package logging

import (
	"io"
	"log/slog"
	"strings"
)

// Redacted replaces every secret found in a log line.
const Redacted = "[REDACTED]"

// New returns a logger that writes one JSON object per line to w, at level
// and above. Every occurrence of a non-empty secret in the message or in a
// string or error attribute is replaced with Redacted, as a last line of
// defence against, for example, a driver error that quotes the database
// password.
func New(w io.Writer, level slog.Leveler, secrets ...string) *slog.Logger {
	var nonEmpty []string
	for _, s := range secrets {
		if s != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}
	opts := &slog.HandlerOptions{Level: level}
	if len(nonEmpty) > 0 {
		opts.ReplaceAttr = func(_ []string, a slog.Attr) slog.Attr {
			return redactAttr(a, nonEmpty)
		}
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}

func redactAttr(a slog.Attr, secrets []string) slog.Attr {
	switch a.Value.Kind() {
	case slog.KindString:
		a.Value = slog.StringValue(redact(a.Value.String(), secrets))
	case slog.KindAny:
		if err, ok := a.Value.Any().(error); ok {
			a.Value = slog.StringValue(redact(err.Error(), secrets))
		}
	}
	return a
}

func redact(s string, secrets []string) string {
	for _, secret := range secrets {
		s = strings.ReplaceAll(s, secret, Redacted)
	}
	return s
}

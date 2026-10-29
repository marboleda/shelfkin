package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"
)

// statusWriter records the status code written through it.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// logRequests logs one line per request with its method, path, status and
// duration.
func logRequests(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			status := sw.status
			if status == 0 {
				// Nothing was written; net/http sends 200.
				status = http.StatusOK
			}
			logger.LogAttrs(r.Context(), slog.LevelInfo, "request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", status),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
			)
		}()
		next.ServeHTTP(sw, r)
	})
}

// recoverPanics turns a panicking handler into a 500 Error response and logs
// the panic with its stack trace. The server keeps running.
func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if v == http.ErrAbortHandler {
				// net/http's own signal to abort the response silently.
				panic(v)
			}
			logger.ErrorContext(r.Context(), "panic serving request",
				"method", r.Method,
				"path", r.URL.Path,
				"panic", fmt.Sprint(v),
				"stack", string(debug.Stack()),
			)
			if sw.status == 0 {
				writeInternal(sw)
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

// discardWriter records the status and headers a handler writes and drops
// the body.
type discardWriter struct {
	header http.Header
	status int
}

func (w *discardWriter) Header() http.Header { return w.header }

func (w *discardWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *discardWriter) Write(b []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	return len(b), nil
}

// jsonFallbacks serves requests that match a route of mux as usual, and
// rewrites the plain-text 404 and 405 responses that mux gives everything
// else into the Error format, keeping the Allow header of a 405.
func jsonFallbacks(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, pattern := mux.Handler(r); pattern != "" {
			mux.ServeHTTP(w, r)
			return
		}
		dw := &discardWriter{header: http.Header{}}
		mux.ServeHTTP(dw, r)
		switch dw.status {
		case http.StatusMethodNotAllowed:
			if allow := dw.header.Get("Allow"); allow != "" {
				w.Header().Set("Allow", allow)
			}
			writeError(w, http.StatusMethodNotAllowed, CodeMethodNotAllowed, "This method is not allowed on this path.")
		default:
			writeError(w, http.StatusNotFound, CodeNotFound, "No endpoint matches this path.")
		}
	})
}

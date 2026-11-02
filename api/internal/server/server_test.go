package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/marboleda/shelfkin/api/db/dbtest"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// syncBuffer is a bytes.Buffer safe for the concurrent writes of a server.
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

// lines returns every logged JSON object.
func (b *syncBuffer) lines(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("log line is not JSON: %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func newLogger() (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// fakeAPI is a StrictServerInterface whose GetHealth runs fn.
type fakeAPI struct {
	fn func(ctx context.Context) (openapi.GetHealthResponseObject, error)
}

func (f fakeAPI) GetHealth(ctx context.Context, _ openapi.GetHealthRequestObject) (openapi.GetHealthResponseObject, error) {
	return f.fn(ctx)
}

func okHealth(context.Context) (openapi.GetHealthResponseObject, error) {
	return openapi.GetHealth200JSONResponse{Status: openapi.HealthStatusOk}, nil
}

// serve runs handler on a real local HTTP server for the test.
func serve(t *testing.T, handler http.Handler) string {
	t.Helper()
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)
	return ts.URL
}

func do(t *testing.T, method, url string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

// assertError checks resp is a JSON Error response with status and code.
func assertError(t *testing.T, resp *http.Response, body []byte, status int, code string) {
	t.Helper()
	if resp.StatusCode != status {
		t.Errorf("status = %d, want %d (body %s)", resp.StatusCode, status, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var e openapi.Error
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&e); err != nil {
		t.Fatalf("body %s is not the Error format: %v", body, err)
	}
	if e.Error.Code != code || e.Error.Message == "" {
		t.Errorf("error = %+v, want code %q and a message", e.Error, code)
	}
}

func TestHealthOKWithDatabase(t *testing.T) {
	pool := dbtest.New(t)
	logger, _ := newLogger()
	url := serve(t, NewHandler(NewAPI(pool, logger), logger))

	// No Authorization header: /health is public.
	resp, body := do(t, http.MethodGet, url+"/health")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("body %s is not JSON: %v", body, err)
	}
	if len(got) != 1 || got["status"] != "ok" {
		t.Errorf("body = %s, want {\"status\":\"ok\"}", body)
	}
}

// refusedAddr returns an address nothing listens on.
func refusedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()
	return addr
}

// silentAddr returns an address that accepts TCP connections and never
// answers, like a database that has hung.
func silentAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	return ln.Addr().String()
}

func TestHealthDatabaseUnavailable(t *testing.T) {
	const password = "unreachable-pw-123"
	tests := []struct {
		name string
		addr func(*testing.T) string
	}{
		{"connection refused", refusedAddr},
		{"database never answers", silentAddr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// connect_timeout is longer than HealthTimeout, so only the
			// handler's own timeout can bound the response.
			dsn := fmt.Sprintf("postgres://shelfkin:%s@%s/shelfkin?sslmode=disable&connect_timeout=30", password, tt.addr(t))
			pool, err := pgxpool.New(context.Background(), dsn)
			if err != nil {
				t.Fatalf("pgxpool.New: %v", err)
			}
			t.Cleanup(pool.Close)
			logger, logs := newLogger()
			url := serve(t, NewHandler(NewAPI(pool, logger), logger))

			start := time.Now()
			resp, body := do(t, http.MethodGet, url+"/health")
			elapsed := time.Since(start)

			assertError(t, resp, body, http.StatusServiceUnavailable, CodeDatabaseUnavailable)
			if limit := HealthTimeout + time.Second; elapsed > limit {
				t.Errorf("took %v, want under %v", elapsed, limit)
			}
			if strings.Contains(logs.String(), password) {
				t.Errorf("logs contain the database password: %s", logs)
			}
		})
	}
}

func TestHealthOtherMethods(t *testing.T) {
	logger, _ := newLogger()
	url := serve(t, NewHandler(fakeAPI{okHealth}, logger))
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			resp, body := do(t, method, url+"/health")
			assertError(t, resp, body, http.StatusMethodNotAllowed, CodeMethodNotAllowed)
			if allow := resp.Header.Get("Allow"); !strings.Contains(allow, http.MethodGet) {
				t.Errorf("Allow = %q, want it to include GET", allow)
			}
		})
	}
}

func TestUnknownPath(t *testing.T) {
	logger, _ := newLogger()
	url := serve(t, NewHandler(fakeAPI{okHealth}, logger))
	for _, path := range []string{"/", "/nope", "/healthz", "/health/extra"} {
		t.Run(path, func(t *testing.T) {
			resp, body := do(t, http.MethodGet, url+path)
			assertError(t, resp, body, http.StatusNotFound, CodeNotFound)
		})
	}
}

func TestPanicReturns500AndServerSurvives(t *testing.T) {
	logger, logs := newLogger()
	var calls int
	var mu sync.Mutex
	url := serve(t, NewHandler(fakeAPI{func(ctx context.Context) (openapi.GetHealthResponseObject, error) {
		mu.Lock()
		calls++
		first := calls == 1
		mu.Unlock()
		if first {
			panic("boom")
		}
		return okHealth(ctx)
	}}, logger))

	resp, body := do(t, http.MethodGet, url+"/health")
	assertError(t, resp, body, http.StatusInternalServerError, CodeInternal)

	var panicLine map[string]any
	for _, l := range logs.lines(t) {
		if l["msg"] == "panic serving request" {
			panicLine = l
		}
	}
	if panicLine == nil {
		t.Fatalf("no panic log line in %s", logs)
	}
	if panicLine["panic"] != "boom" || panicLine["level"] != "ERROR" {
		t.Errorf("panic line = %v", panicLine)
	}
	if stack, _ := panicLine["stack"].(string); !strings.Contains(stack, "server_test.go") {
		t.Errorf("panic line has no stack trace through the handler: %v", panicLine["stack"])
	}

	resp, body = do(t, http.MethodGet, url+"/health")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("after a panic: status = %d, want 200 (body %s)", resp.StatusCode, body)
	}
}

func TestHandlerErrorReturns500(t *testing.T) {
	logger, _ := newLogger()
	url := serve(t, NewHandler(fakeAPI{func(context.Context) (openapi.GetHealthResponseObject, error) {
		return nil, errors.New("something broke")
	}}, logger))
	resp, body := do(t, http.MethodGet, url+"/health")
	assertError(t, resp, body, http.StatusInternalServerError, CodeInternal)
}

func TestRequestLogging(t *testing.T) {
	logger, logs := newLogger()
	url := serve(t, NewHandler(fakeAPI{okHealth}, logger))
	do(t, http.MethodGet, url+"/health")
	do(t, http.MethodGet, url+"/missing")

	var requests []map[string]any
	for _, l := range logs.lines(t) {
		if l["msg"] == "request" {
			requests = append(requests, l)
		}
	}
	if len(requests) != 2 {
		t.Fatalf("got %d request lines, want 2: %s", len(requests), logs)
	}
	want := []struct {
		path   string
		status float64
	}{{"/health", 200}, {"/missing", 404}}
	for i, l := range requests {
		if l["method"] != "GET" || l["path"] != want[i].path || l["status"] != want[i].status {
			t.Errorf("request line %d = %v, want GET %s %v", i, l, want[i].path, want[i].status)
		}
		if _, ok := l["duration_ms"].(float64); !ok {
			t.Errorf("request line %d has no numeric duration_ms: %v", i, l)
		}
	}
}

func TestHTTPServerTimeouts(t *testing.T) {
	logger, _ := newLogger()
	srv := NewHTTPServer(http.NotFoundHandler(), logger)
	for name, d := range map[string]time.Duration{
		"ReadHeaderTimeout": srv.ReadHeaderTimeout,
		"ReadTimeout":       srv.ReadTimeout,
		"WriteTimeout":      srv.WriteTimeout,
		"IdleTimeout":       srv.IdleTimeout,
	} {
		if d <= 0 {
			t.Errorf("%s = %v, want > 0", name, d)
		}
	}
	if srv.WriteTimeout <= HealthTimeout {
		t.Errorf("WriteTimeout %v must exceed HealthTimeout %v", srv.WriteTimeout, HealthTimeout)
	}
}

// startServe runs Serve with a handler that blocks until release is closed,
// and returns the server URL, a channel that receives each time a request
// enters the handler, and Serve's result.
func startServe(t *testing.T, shutdownTimeout time.Duration, release <-chan struct{}) (string, <-chan struct{}, context.CancelFunc, <-chan error) {
	t.Helper()
	entered := make(chan struct{}, 1)
	logger, _ := newLogger()
	handler := NewHandler(fakeAPI{func(ctx context.Context) (openapi.GetHealthResponseObject, error) {
		entered <- struct{}{}
		<-release
		return okHealth(ctx)
	}}, logger)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, NewHTTPServer(handler, logger), ln, shutdownTimeout) }()
	return "http://" + ln.Addr().String(), entered, cancel, done
}

func TestServeFinishesInFlightRequestOnShutdown(t *testing.T) {
	release := make(chan struct{})
	url, entered, shutdown, done := startServe(t, 5*time.Second, release)

	type result struct {
		status int
		body   string
		err    error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.Get(url + "/health")
		if err != nil {
			got <- result{err: err}
			return
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		got <- result{resp.StatusCode, string(b), err}
	}()

	<-entered  // the request is in flight
	shutdown() // as on SIGTERM

	// Shutdown must wait for the request rather than return.
	select {
	case err := <-done:
		t.Fatalf("Serve returned %v while a request was in flight", err)
	case <-time.After(200 * time.Millisecond):
	}
	// New connections are refused once shutdown has started.
	if c, err := net.DialTimeout("tcp", strings.TrimPrefix(url, "http://"), time.Second); err == nil {
		c.Close()
		t.Error("server still accepts new connections after shutdown started")
	}

	close(release)
	r := <-got
	if r.err != nil {
		t.Fatalf("in-flight request failed: %v", r.err)
	}
	if r.status != http.StatusOK || !strings.Contains(r.body, `"ok"`) {
		t.Errorf("in-flight request got %d %s, want 200 ok", r.status, r.body)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve = %v, want nil after a clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after the in-flight request finished")
	}
}

func TestServeShutdownTimeout(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	url, entered, shutdown, done := startServe(t, 100*time.Millisecond, release)

	go func() {
		resp, err := http.Get(url + "/health")
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-entered
	shutdown()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Serve = %v, want context.DeadlineExceeded", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not give up after the shutdown timeout")
	}
}

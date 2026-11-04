package server

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/marboleda/shelfkin/api/internal/openapi"
)

const (
	allowedOrigin = "https://shelfkin.web.app"
	otherAllowed  = "http://localhost:8081"
)

var testOrigins = []string{allowedOrigin, otherAllowed}

// doWithHeaders sends a request with the given headers and returns the
// response and its body.
func doWithHeaders(t *testing.T, method, url string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
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

// splitList returns the comma-separated values of every header line named
// key, trimmed.
func splitList(h http.Header, key string) []string {
	var out []string
	for _, line := range h.Values(key) {
		for _, v := range strings.Split(line, ",") {
			if v = strings.TrimSpace(v); v != "" {
				out = append(out, v)
			}
		}
	}
	return out
}

func assertAllowsOrigin(t *testing.T, resp *http.Response, origin string) {
	t.Helper()
	if got := resp.Header.Values("Access-Control-Allow-Origin"); len(got) != 1 || got[0] != origin {
		t.Errorf("Access-Control-Allow-Origin = %q, want exactly %q", got, origin)
	}
	if !slices.Contains(splitList(resp.Header, "Vary"), "Origin") {
		t.Errorf("Vary = %q, want it to include Origin", resp.Header.Values("Vary"))
	}
}

func assertNoCORS(t *testing.T, resp *http.Response) {
	t.Helper()
	for _, h := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Methods",
		"Access-Control-Allow-Headers",
		"Access-Control-Allow-Credentials",
		"Access-Control-Max-Age",
	} {
		if v := resp.Header.Values(h); len(v) != 0 {
			t.Errorf("%s = %q, want no such header", h, v)
		}
	}
}

func TestCORSAllowedOrigin(t *testing.T) {
	logger, _ := newLogger()
	url := serve(t, NewHandler(fakeAPI{okHealth}, denyAll, logger, testOrigins))
	for _, origin := range testOrigins {
		t.Run(origin, func(t *testing.T) {
			resp, body := doWithHeaders(t, http.MethodGet, url+"/health", map[string]string{
				"Origin":        origin,
				"Authorization": "Bearer token",
			})
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200 (body %s)", resp.StatusCode, body)
			}
			if !strings.Contains(string(body), `"ok"`) {
				t.Errorf("body = %s, want the health response", body)
			}
			assertAllowsOrigin(t, resp, origin)
		})
	}
}

func TestCORSDisallowedOrigin(t *testing.T) {
	logger, _ := newLogger()
	url := serve(t, NewHandler(fakeAPI{okHealth}, denyAll, logger, testOrigins))
	for _, origin := range []string{
		"https://evil.example",
		"https://shelfkin.web.app.evil.example",
		"http://shelfkin.web.app",   // wrong scheme
		"http://localhost:8082",     // wrong port
		"https://shelfkin.web.app/", // not an origin a browser sends
		"HTTPS://SHELFKIN.WEB.APP",
		"null",
		"*",
	} {
		t.Run(origin, func(t *testing.T) {
			resp, body := doWithHeaders(t, http.MethodGet, url+"/health", map[string]string{"Origin": origin})
			// The request is still served; only the browser blocks reading it.
			if resp.StatusCode != http.StatusOK {
				t.Errorf("status = %d, want 200 (body %s)", resp.StatusCode, body)
			}
			assertNoCORS(t, resp)
			if !slices.Contains(splitList(resp.Header, "Vary"), "Origin") {
				t.Errorf("Vary = %q, want it to include Origin", resp.Header.Values("Vary"))
			}
		})
	}
}

func TestCORSNoOrigin(t *testing.T) {
	logger, _ := newLogger()
	url := serve(t, NewHandler(fakeAPI{okHealth}, denyAll, logger, testOrigins))
	resp, _ := do(t, http.MethodGet, url+"/health")
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	assertNoCORS(t, resp)
}

func TestCORSPreflightAllowedOrigin(t *testing.T) {
	logger, logs := newLogger()
	url := serve(t, NewHandler(fakeAPI{okHealth}, denyAll, logger, testOrigins))
	// /health is GET-only and /households does not exist yet: a preflight is
	// answered before routing, for any path.
	for _, tc := range []struct{ path, method string }{
		{"/health", http.MethodGet},
		{"/households", http.MethodPost},
		{"/households/abc", http.MethodPatch},
		{"/households/abc", http.MethodDelete},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			resp, body := doWithHeaders(t, http.MethodOptions, url+tc.path, map[string]string{
				"Origin":                         allowedOrigin,
				"Access-Control-Request-Method":  tc.method,
				"Access-Control-Request-Headers": "authorization,content-type",
			})
			if resp.StatusCode < 200 || resp.StatusCode > 299 {
				t.Fatalf("status = %d, want 2xx (body %s)", resp.StatusCode, body)
			}
			assertAllowsOrigin(t, resp, allowedOrigin)
			methods := splitList(resp.Header, "Access-Control-Allow-Methods")
			for _, m := range []string{"GET", "POST", "PATCH", "DELETE"} {
				if !slices.Contains(methods, m) {
					t.Errorf("Access-Control-Allow-Methods = %q, missing %s", methods, m)
				}
			}
			headers := splitList(resp.Header, "Access-Control-Allow-Headers")
			for _, h := range []string{"Authorization", "Content-Type"} {
				if !slices.ContainsFunc(headers, func(v string) bool { return strings.EqualFold(v, h) }) {
					t.Errorf("Access-Control-Allow-Headers = %q, missing %s", headers, h)
				}
			}
			if resp.Header.Get("Access-Control-Max-Age") == "" {
				t.Error("no Access-Control-Max-Age")
			}
			if v := resp.Header.Get("Access-Control-Allow-Credentials"); v != "" {
				t.Errorf("Access-Control-Allow-Credentials = %q, want none: auth is a bearer token", v)
			}
			vary := splitList(resp.Header, "Vary")
			for _, v := range []string{"Origin", "Access-Control-Request-Method", "Access-Control-Request-Headers"} {
				if !slices.Contains(vary, v) {
					t.Errorf("Vary = %q, missing %s", vary, v)
				}
			}
		})
	}
	// Preflights are logged like any other request.
	var found bool
	for _, l := range logs.lines(t) {
		if l["msg"] == "request" && l["method"] == "OPTIONS" && l["status"] == float64(http.StatusNoContent) {
			found = true
		}
	}
	if !found {
		t.Errorf("no request log line for a preflight in %s", logs)
	}
}

func TestCORSPreflightDisallowedOrigin(t *testing.T) {
	logger, _ := newLogger()
	url := serve(t, NewHandler(fakeAPI{okHealth}, denyAll, logger, testOrigins))
	resp, body := doWithHeaders(t, http.MethodOptions, url+"/health", map[string]string{
		"Origin":                         "https://evil.example",
		"Access-Control-Request-Method":  http.MethodGet,
		"Access-Control-Request-Headers": "authorization",
	})
	assertNoCORS(t, resp)
	// It falls through to the router, which has no OPTIONS route.
	assertError(t, resp, body, http.StatusMethodNotAllowed, CodeMethodNotAllowed)
}

func TestCORSPlainOptionsIsNotAPreflight(t *testing.T) {
	logger, _ := newLogger()
	url := serve(t, NewHandler(fakeAPI{okHealth}, denyAll, logger, testOrigins))
	// No Access-Control-Request-Method: an ordinary OPTIONS request.
	resp, body := doWithHeaders(t, http.MethodOptions, url+"/health", map[string]string{"Origin": allowedOrigin})
	assertError(t, resp, body, http.StatusMethodNotAllowed, CodeMethodNotAllowed)
	assertAllowsOrigin(t, resp, allowedOrigin)
	if v := resp.Header.Get("Access-Control-Allow-Methods"); v != "" {
		t.Errorf("Access-Control-Allow-Methods = %q on a non-preflight request", v)
	}
}

func TestCORSHeadersOnErrorResponses(t *testing.T) {
	logger, _ := newLogger()
	failing := fakeAPI{func(context.Context) (openapi.GetHealthResponseObject, error) {
		return nil, errors.New("something broke")
	}}
	panicking := fakeAPI{func(context.Context) (openapi.GetHealthResponseObject, error) {
		panic("boom")
	}}
	tests := []struct {
		name   string
		ssi    openapi.StrictServerInterface
		method string
		path   string
		status int
		code   string
	}{
		{"404 unknown path", fakeAPI{okHealth}, http.MethodGet, "/nope", http.StatusNotFound, CodeNotFound},
		{"405 wrong method", fakeAPI{okHealth}, http.MethodDelete, "/health", http.StatusMethodNotAllowed, CodeMethodNotAllowed},
		{"500 handler error", failing, http.MethodGet, "/health", http.StatusInternalServerError, CodeInternal},
		{"500 panic", panicking, http.MethodGet, "/health", http.StatusInternalServerError, CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			url := serve(t, NewHandler(tt.ssi, denyAll, logger, testOrigins))
			resp, body := doWithHeaders(t, tt.method, url+tt.path, map[string]string{"Origin": allowedOrigin})
			assertError(t, resp, body, tt.status, tt.code)
			assertAllowsOrigin(t, resp, allowedOrigin)
		})
	}
}

// TestCORSOnHandWrittenErrors covers statuses no route returns yet, such as
// the 401 of the auth middleware: whatever a wrapped handler writes carries
// the CORS headers.
func TestCORSOnHandWrittenErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			h := cors(testOrigins, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				writeError(w, status, "some_code", "Some message.")
			}))
			req := httptest.NewRequest(http.MethodGet, "/anything", nil)
			req.Header.Set("Origin", otherAllowed)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			resp := rec.Result()
			if resp.StatusCode != status {
				t.Errorf("status = %d, want %d", resp.StatusCode, status)
			}
			assertAllowsOrigin(t, resp, otherAllowed)
		})
	}
}

func TestCORSNoOriginsAllowed(t *testing.T) {
	logger, _ := newLogger()
	url := serve(t, NewHandler(fakeAPI{okHealth}, denyAll, logger, nil))
	resp, _ := doWithHeaders(t, http.MethodGet, url+"/health", map[string]string{"Origin": allowedOrigin})
	assertNoCORS(t, resp)
}

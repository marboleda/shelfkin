package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"gopkg.in/yaml.v3"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

const secretToken = "eyJhbGciOiJub25lIn0.c2VjcmV0LXRva2VuLXZhbHVl.sig"

func TestBearerToken(t *testing.T) {
	tests := []struct {
		header string
		want   string
		ok     bool
	}{
		{"Bearer abc.def.ghi", "abc.def.ghi", true},
		{"bearer abc", "abc", true},
		{"BEARER abc", "abc", true},
		{"Bearer  abc", "abc", true},
		{"", "", false},
		{"Bearer", "", false},
		{"Bearer ", "", false},
		{"Bearer    ", "", false},
		{"Basic dXNlcjpwYXNz", "", false},
		{"Token abc", "", false},
		{"abc", "", false},
		{"Bearerabc", "", false},
		{"Bearer abc def", "", false},
		{"Bearer abc\tdef", "", false},
	}
	for _, tt := range tests {
		got, ok := bearerToken(tt.header)
		if got != tt.want || ok != tt.ok {
			t.Errorf("bearerToken(%q) = %q, %v; want %q, %v", tt.header, got, ok, tt.want, tt.ok)
		}
	}
}

// recordingAuth accepts one token and records every token it is given.
type recordingAuth struct {
	valid  string
	userID pgtype.UUID
	err    error // returned for any other token; ErrInvalidToken if nil
	seen   []string
}

func (a *recordingAuth) Authenticate(_ context.Context, token string) (pgtype.UUID, error) {
	a.seen = append(a.seen, token)
	if token == a.valid {
		return a.userID, nil
	}
	if a.err != nil {
		return pgtype.UUID{}, a.err
	}
	return pgtype.UUID{}, auth.ErrInvalidToken
}

func TestRequireAuthRejects(t *testing.T) {
	logger, logs := newLogger()
	authn := &recordingAuth{valid: "good", userID: pgtype.UUID{Bytes: [16]byte{9}, Valid: true}}
	url := serve(t, NewHandler(fakeAPI{okHealth}, authn, logger, nil))

	for _, tt := range []struct {
		name   string
		header string
	}{
		{"no header", ""},
		{"empty bearer", "Bearer "},
		{"wrong scheme", "Basic " + secretToken},
		{"no scheme", secretToken},
		{"token with a space", "Bearer " + secretToken + " extra"},
		{"token the verifier rejects", "Bearer " + secretToken},
	} {
		for _, method := range []string{http.MethodGet, http.MethodPatch} {
			t.Run(tt.name+" "+method, func(t *testing.T) {
				headers := map[string]string{}
				if tt.header != "" {
					headers["Authorization"] = tt.header
				}
				resp, body := doWithHeaders(t, method, url+"/me", headers)
				assertError(t, resp, body, http.StatusUnauthorized, CodeUnauthorized)
				if strings.Contains(string(body), secretToken) || strings.Contains(string(body), "c2VjcmV0") {
					t.Errorf("401 body echoes the token: %s", body)
				}
			})
		}
	}
	if strings.Contains(logs.String(), secretToken) {
		t.Errorf("logs contain the token: %s", logs)
	}
	// Only well-formed bearer headers reach the authenticator.
	for _, tok := range authn.seen {
		if tok != secretToken {
			t.Errorf("authenticator was called with %q", tok)
		}
	}
}

func TestRequireAuthPassesUserIDToHandler(t *testing.T) {
	logger, _ := newLogger()
	userID := pgtype.UUID{Bytes: [16]byte{1, 2, 3, 4}, Valid: true}
	authn := &recordingAuth{valid: "good-token", userID: userID}
	var got pgtype.UUID
	var gotOK bool
	api := meProbe{onGetMe: func(ctx context.Context) { got, gotOK = auth.UserID(ctx) }}
	url := serve(t, NewHandler(api, authn, logger, nil))

	resp, body := doWithHeaders(t, http.MethodGet, url+"/me", map[string]string{"Authorization": "Bearer good-token"})
	if resp.StatusCode != http.StatusInternalServerError {
		// meProbe returns an error after recording; only the context matters.
		t.Fatalf("status = %d, want the probe's 500 (body %s)", resp.StatusCode, body)
	}
	if !gotOK || got != userID {
		t.Errorf("handler saw user %v (ok %v), want %v", got, gotOK, userID)
	}
}

// meProbe records the context GET /me runs with.
type meProbe struct {
	fakeAPI
	onGetMe func(context.Context)
}

func (p meProbe) GetMe(ctx context.Context, _ openapi.GetMeRequestObject) (openapi.GetMeResponseObject, error) {
	p.onGetMe(ctx)
	return nil, errors.New("probe")
}

func TestRequireAuthAuthenticatorFailureIs500(t *testing.T) {
	logger, logs := newLogger()
	authn := &recordingAuth{err: errors.New("database down")}
	url := serve(t, NewHandler(fakeAPI{okHealth}, authn, logger, nil))
	resp, body := doWithHeaders(t, http.MethodGet, url+"/me", map[string]string{"Authorization": "Bearer " + secretToken})
	assertError(t, resp, body, http.StatusInternalServerError, CodeInternal)
	if !strings.Contains(logs.String(), "database down") {
		t.Errorf("the failure was not logged: %s", logs)
	}
	if strings.Contains(logs.String(), secretToken) {
		t.Errorf("logs contain the token: %s", logs)
	}
}

func TestHealthNeedsNoToken(t *testing.T) {
	logger, _ := newLogger()
	authn := &recordingAuth{}
	url := serve(t, NewHandler(fakeAPI{okHealth}, authn, logger, nil))
	for _, header := range []string{"", "Bearer garbage", "Basic x"} {
		resp, body := doWithHeaders(t, http.MethodGet, url+"/health", map[string]string{"Authorization": header})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Authorization %q: status = %d, want 200 (body %s)", header, resp.StatusCode, body)
		}
	}
	if len(authn.seen) != 0 {
		t.Errorf("/health called the authenticator with %q", authn.seen)
	}
}

// TestUnknownRoutesAre404Not401: authentication applies to endpoints, so an
// unknown path or method still gets its 404 or 405.
func TestUnknownRoutesAre404Not401(t *testing.T) {
	logger, _ := newLogger()
	url := serve(t, NewHandler(fakeAPI{okHealth}, denyAll, logger, nil))
	resp, body := do(t, http.MethodGet, url+"/nope")
	assertError(t, resp, body, http.StatusNotFound, CodeNotFound)
	resp, body = do(t, http.MethodDelete, url+"/me")
	assertError(t, resp, body, http.StatusMethodNotAllowed, CodeMethodNotAllowed)
}

// TestPublicRoutesMatchSpec keeps PublicRoutes equal to the operations that
// set `security: []` in openapi/openapi.yaml, and checks the spec requires
// the bearer token everywhere else.
func TestPublicRoutesMatchSpec(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "openapi", "openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Security []map[string][]string           `yaml:"security"`
		Paths    map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Security) != 1 || spec.Security[0]["firebaseAuth"] == nil {
		t.Fatalf("top-level security = %v, want firebaseAuth", spec.Security)
	}
	methods := []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}
	var public []string
	for path, item := range spec.Paths {
		for method, node := range item {
			if !slices.Contains(methods, method) {
				continue // summary, parameters, ...
			}
			var op struct {
				Security *[]any `yaml:"security"`
			}
			if err := node.Decode(&op); err != nil {
				t.Fatalf("%s %s: %v", method, path, err)
			}
			if op.Security == nil {
				continue // inherits firebaseAuth
			}
			if len(*op.Security) != 0 {
				t.Errorf("%s %s sets its own security %v; the middleware only knows firebaseAuth and none", method, path, *op.Security)
				continue
			}
			public = append(public, strings.ToUpper(method)+" "+path)
		}
	}
	var routes []string
	for r := range PublicRoutes {
		routes = append(routes, r)
	}
	slices.Sort(public)
	slices.Sort(routes)
	if !slices.Equal(public, routes) {
		t.Errorf("PublicRoutes = %q, but the spec's public operations are %q", routes, public)
	}
}

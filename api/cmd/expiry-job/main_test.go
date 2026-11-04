package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/marboleda/shelfkin/api/db/dbtest"
)

func env(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func fixedClock() time.Time { return time.Date(2026, 10, 27, 13, 0, 0, 0, time.UTC) }

// databaseURL returns a connection string for the pool's database.
func databaseURL(pool *pgxpool.Pool) string {
	c := pool.Config().ConnConfig
	u := url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(c.User, c.Password),
		Host:     net.JoinHostPort(c.Host, strconv.Itoa(int(c.Port))),
		Path:     "/" + c.Database,
		RawQuery: "sslmode=disable",
	}
	return u.String()
}

// fakeExpo answers every message with an ok ticket and records requests.
type fakeExpo struct {
	mu       sync.Mutex
	messages []map[string]any
	auth     []string
}

func (f *fakeExpo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var msgs []map[string]any
	if err := json.NewDecoder(r.Body).Decode(&msgs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.messages = append(f.messages, msgs...)
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.mu.Unlock()
	tickets := make([]map[string]string, len(msgs))
	for i := range tickets {
		tickets[i] = map[string]string{"status": "ok", "id": fmt.Sprint(i)}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": tickets})
}

// seed adds a Toronto user with one token and one expiring entry.
func seed(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
		WITH u AS (
		    INSERT INTO users (firebase_uid, display_name, timezone, push_enabled)
		    VALUES ('fb-1', 'Sam', 'America/Toronto', true) RETURNING id
		), t AS (
		    INSERT INTO push_tokens (user_id, token) SELECT id, 'ExponentPushToken[cmd]' FROM u
		), h AS (
		    INSERT INTO households (name) VALUES ('Home') RETURNING id
		), m AS (
		    INSERT INTO household_members (household_id, user_id, role) SELECT h.id, u.id, 'owner' FROM h, u
		), c AS (
		    INSERT INTO catalog_items (household_id, name, default_category_id, default_location_id)
		    SELECT id, 'Milk', 1, 2 FROM h RETURNING id, household_id
		)
		INSERT INTO pantry_entries (id, household_id, catalog_item_id, location_id, level, expires_on)
		SELECT uuidv7(), household_id, id, 2, 'full', '2026-10-28' FROM c`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestRunSendsAndExits(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	seed(t, pool)
	expo := &fakeExpo{}
	srv := httptest.NewServer(expo)
	defer srv.Close()
	vars := map[string]string{
		"DATABASE_URL":      databaseURL(pool),
		"EXPO_PUSH_URL":     srv.URL,
		"EXPO_ACCESS_TOKEN": "expo-secret-token",
		"LOG_LEVEL":         "debug",
	}

	// --dry-run sends and writes nothing.
	var out, errOut bytes.Buffer
	if code := run(t.Context(), []string{"--now", "2026-10-27T13:00:00Z", "--dry-run"}, env(vars), &out, &errOut, time.Now); code != 0 {
		t.Fatalf("dry run exit code %d\n%s%s", code, out.String(), errOut.String())
	}
	if len(expo.messages) != 0 {
		t.Errorf("dry run sent %d messages", len(expo.messages))
	}
	if !strings.Contains(out.String(), "dry run: would send expiry push") {
		t.Errorf("dry run logged nothing to send:\n%s", out.String())
	}

	// --now picks the hour: 12:00 UTC is 08:00 in Toronto.
	out.Reset()
	if code := run(t.Context(), []string{"--now", "2026-10-27T12:00:00Z"}, env(vars), &out, &errOut, time.Now); code != 0 {
		t.Fatalf("exit code %d\n%s", code, out.String())
	}
	if len(expo.messages) != 0 {
		t.Errorf("sent %d messages at 08:00 local", len(expo.messages))
	}

	// Without --now, the clock is used.
	out.Reset()
	if code := run(t.Context(), nil, env(vars), &out, &errOut, fixedClock); code != 0 {
		t.Fatalf("exit code %d\n%s", code, out.String())
	}
	if len(expo.messages) != 1 || expo.messages[0]["title"] != "Expiring soon in Home" || expo.messages[0]["body"] != "Milk" {
		t.Errorf("Expo got %v", expo.messages)
	}
	if len(expo.auth) != 1 || expo.auth[0] != "Bearer expo-secret-token" {
		t.Errorf("Authorization headers %q", expo.auth)
	}
	if !strings.Contains(out.String(), `"users_processed":1,"pushes_sent":1,"failures":0`) {
		t.Errorf("no summary line:\n%s", out.String())
	}
	if strings.Contains(out.String(), "expo-secret-token") {
		t.Errorf("logs contain the access token:\n%s", out.String())
	}

	// Running again in the same hour sends nothing.
	out.Reset()
	if code := run(t.Context(), nil, env(vars), &out, &errOut, fixedClock); code != 0 {
		t.Fatalf("exit code %d\n%s", code, out.String())
	}
	if len(expo.messages) != 1 {
		t.Errorf("second run sent again: %d messages in total", len(expo.messages))
	}
}

func TestRunExitsNonZeroOnFailure(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	seed(t, pool)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	vars := map[string]string{"DATABASE_URL": databaseURL(pool), "EXPO_PUSH_URL": srv.URL}

	var out, errOut bytes.Buffer
	if code := run(t.Context(), nil, env(vars), &out, &errOut, fixedClock); code != 1 {
		t.Fatalf("exit code %d, want 1\n%s", code, out.String())
	}
	if !strings.Contains(out.String(), `"failures":1`) {
		t.Errorf("summary does not count the failure:\n%s", out.String())
	}
}

func TestRunUsageErrors(t *testing.T) {
	t.Parallel()
	vars := map[string]string{"DATABASE_URL": "postgres://u:p@localhost:5432/db"}
	for _, tc := range []struct {
		name string
		args []string
		vars map[string]string
		want int
	}{
		{"bad --now", []string{"--now", "yesterday"}, vars, 2},
		{"unknown flag", []string{"--later"}, vars, 2},
		{"extra argument", []string{"go"}, vars, 2},
		{"missing DATABASE_URL", nil, map[string]string{}, 1},
		{"bad EXPO_PUSH_URL", nil, map[string]string{"DATABASE_URL": vars["DATABASE_URL"], "EXPO_PUSH_URL": "exp.host"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var out, errOut bytes.Buffer
			if code := run(t.Context(), tc.args, env(tc.vars), &out, &errOut, fixedClock); code != tc.want {
				t.Errorf("exit code %d, want %d\nstdout: %s\nstderr: %s", code, tc.want, out.String(), errOut.String())
			}
		})
	}
}

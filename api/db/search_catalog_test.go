package db_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/marboleda/shelfkin/api/db/dbtest"
	"github.com/marboleda/shelfkin/api/internal/db"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
)

// searchAs runs the SearchCatalog query through dbtx.WithUser as user.
func searchAs(ctx context.Context, t *testing.T, pool *pgxpool.Pool, user, household, q string, limit int32) ([]db.SearchCatalogRow, error) {
	var rows []db.SearchCatalogRow
	err := dbtx.WithUser(ctx, pool, pgUUID(t, user), func(tx pgx.Tx) error {
		var err error
		rows, err = db.New(tx).SearchCatalog(ctx, db.SearchCatalogParams{
			HouseholdID: pgUUID(t, household),
			Q:           q,
			MaxResults:  limit,
		})
		return err
	})
	return rows, err
}

// TestSearchCatalogQueryRows checks the three shapes of the SearchCatalog
// result that the handler relies on: no row for a non-member or a missing
// household, one row with a NULL item for a member with no matches (or an
// empty q), and one row per match otherwise.
func TestSearchCatalogQueryRows(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)
	alice := insertUser(t, pool, "alice")
	bob := insertUser(t, pool, "bob")
	home := insertHousehold(t, pool, "Home", alice)
	if err := insertMember(ctx, pool, home, alice, "owner"); err != nil {
		t.Fatal(err)
	}
	other := insertHousehold(t, pool, "Other", nil)

	for _, c := range []struct {
		name, user, household, q string
	}{
		{"non-member", bob, home, "milk"},
		{"non-member, empty q", bob, home, ""},
		{"household with no members", alice, other, "milk"},
		{"missing household", alice, "00000000-0000-7000-8000-000000000000", "milk"},
	} {
		rows, err := searchAs(ctx, t, pool, c.user, c.household, c.q, 25)
		if err != nil || len(rows) != 0 {
			t.Errorf("%s: %d rows, %v; want no rows", c.name, len(rows), err)
		}
	}
	for _, q := range []string{"", "zzzzqqq", "mk"} {
		rows, err := searchAs(ctx, t, pool, alice, home, q, 25)
		if err != nil || len(rows) != 1 || rows[0].CatalogItemID.Valid {
			t.Errorf("member, q=%q: %+v, %v; want one row with no item", q, rows, err)
		}
	}
	rows, err := searchAs(ctx, t, pool, alice, home, "milk", 3)
	if err != nil || len(rows) != 3 {
		t.Fatalf("member, q=milk, limit 3: %d rows, %v; want 3", len(rows), err)
	}
	for _, r := range rows {
		if !r.CatalogItemID.Valid {
			t.Errorf("member, q=milk: a row with no item: %+v", rows)
		}
	}
}

// Latency target of the search (#17), and how it is measured.
const (
	searchP95Target  = 20 * time.Millisecond
	searchRunsPerQ   = 25
	searchWarmUpRuns = 6
	perfEnv          = "SHELFKIN_PERF"
	customPerHouse   = 100
	searchPerfPlanQ  = "tom"
	searchPerfMember = "perf-1"
)

// loadCustomItems adds that many users and households (each owned by its
// user) and customPerHouse custom items per household, named like
// "Smoked tomatoes 42" from the built-in names, then runs VACUUM ANALYZE
// (ANALYZE for the planner; VACUUM so autovacuum does not start on the
// freshly loaded tables while the test is timing).
func loadCustomItems(t *testing.T, pool *pgxpool.Pool, households int) {
	t.Helper()
	_, err := pool.Exec(t.Context(), `
		INSERT INTO users (firebase_uid, display_name, timezone)
		SELECT 'perf-' || g, 'Perf ' || g, 'UTC' FROM generate_series(1, $1::integer) g`, households)
	if err != nil {
		t.Fatalf("loading users: %v", err)
	}
	_, err = pool.Exec(t.Context(), `
		INSERT INTO households (name, created_by)
		SELECT 'House ' || u.display_name, u.id FROM users u WHERE u.firebase_uid LIKE 'perf-%';
		INSERT INTO household_members (household_id, user_id, role)
		SELECT h.id, h.created_by, 'owner' FROM households h;`)
	if err != nil {
		t.Fatalf("loading households: %v", err)
	}
	_, err = pool.Exec(t.Context(), `
		WITH adj(a) AS (VALUES ('Homemade'), ('Spicy'), ('Organic'), ('Smoked'), ('Sweet'),
		                       ('Grandma''s'), ('Roasted'), ('Pickled'), ('Wild'), ('Frozen')),
		     noun(n) AS (SELECT name FROM catalog_items WHERE household_id IS NULL)
		INSERT INTO catalog_items (household_id, name, default_category_id, default_location_id)
		SELECT h.id, x.nm, 1, 1
		FROM (SELECT id, row_number() OVER (ORDER BY id) AS rn FROM households) h
		CROSS JOIN LATERAL (
		    SELECT a || ' ' || lower(n) || ' ' || g AS nm
		    FROM (SELECT a, n, row_number() OVER (ORDER BY md5(a || n || h.rn::text)) AS g
		          FROM adj, noun) z
		    WHERE g <= $1::integer
		) x`, customPerHouse)
	if err != nil {
		t.Fatalf("loading custom items: %v", err)
	}
	if _, err := pool.Exec(t.Context(), `VACUUM ANALYZE`); err != nil {
		t.Fatal(err)
	}
	if n, want := count(t, pool, `SELECT count(*) FROM catalog_items WHERE household_id IS NOT NULL`), households*customPerHouse; n != want {
		t.Fatalf("%d custom items, want %d", n, want)
	}
}

// lastQuery is a pgx tracer that keeps the last statement and its arguments.
type lastQuery struct {
	mu   sync.Mutex
	sql  string
	args []any
}

func (l *lastQuery) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	if strings.Contains(d.SQL, "effective_catalog_items") {
		l.sql, l.args = d.SQL, d.Args
	}
	return ctx
}

func (*lastQuery) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// p95 returns the 95th percentile of d (nearest rank).
func p95(d []time.Duration) time.Duration {
	s := slices.Clone(d)
	slices.Sort(s)
	return s[int(math.Ceil(0.95*float64(len(s))))-1]
}

// TestSearchCatalogLatency measures the search's latency at the two dataset
// sizes of #17, through dbtx.WithUser as a member (begin, role and
// app.user_id setup, the query, commit), after VACUUM ANALYZE, with no planner
// settings changed. It fails if the p95 of any q is 20 ms or more, and logs
// each p95 and one EXPLAIN ANALYZE plan per size. Timing depends on the
// machine, so it runs only with SHELFKIN_PERF=1:
//
//	SHELFKIN_PERF=1 go test ./db -run TestSearchCatalogLatency -v
func TestSearchCatalogLatency(t *testing.T) {
	if os.Getenv(perfEnv) != "1" {
		t.Skipf("set %s=1 to measure the search latency", perfEnv)
	}
	for _, households := range []int{100, 1000} {
		t.Run(fmt.Sprintf("%d custom items over %d households", households*customPerHouse, households), func(t *testing.T) {
			ctx := t.Context()
			base := dbtest.New(t)
			loadCustomItems(t, base, households)

			cfg := base.Config()
			tracer := &lastQuery{}
			cfg.ConnConfig.Tracer = tracer
			cfg.MaxConns = 1
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(pool.Close)

			var household, user string
			err = base.QueryRow(ctx,
				`SELECT h.id, u.id FROM households h JOIN users u ON u.id = h.created_by WHERE u.firebase_uid = $1`,
				searchPerfMember).Scan(&household, &user)
			if err != nil {
				t.Fatal(err)
			}

			for _, q := range []string{"to", "tom", "tomato"} {
				// A few untimed runs to warm the connection, the statement
				// cache and the server's plan cache (which may switch to a
				// generic plan after five executions).
				for range searchWarmUpRuns {
					if rows, err := searchAs(ctx, t, pool, user, household, q, 10); err != nil || len(rows) == 0 || !rows[0].CatalogItemID.Valid {
						t.Fatalf("q=%q: %d rows, %v; want matches", q, len(rows), err)
					}
				}
				times := make([]time.Duration, 0, searchRunsPerQ)
				for range searchRunsPerQ {
					start := time.Now()
					if _, err := searchAs(ctx, t, pool, user, household, q, 10); err != nil {
						t.Fatal(err)
					}
					times = append(times, time.Since(start))
				}
				got := p95(times)
				t.Logf("q=%-6q runs=%d p95=%.3f ms (min %.3f ms, max %.3f ms)", q, len(times),
					ms(got), ms(slices.Min(times)), ms(slices.Max(times)))
				// Every run in order, so a burst of slow runs from the
				// machine being busy is visible.
				t.Logf("  runs: %v", times)
				if got >= searchP95Target {
					t.Errorf("q=%q: p95 %v, want under %v", q, got, searchP95Target)
				}
			}

			// For comparison (not checked): the same transaction with
			// SELECT 1 instead of the search, which is the cost of dbtx and
			// the round trips alone.
			baseline := make([]time.Duration, 0, searchRunsPerQ)
			for range searchRunsPerQ {
				start := time.Now()
				err := dbtx.WithUser(ctx, pool, pgUUID(t, user), func(tx pgx.Tx) error {
					_, err := tx.Exec(ctx, `SELECT 1`)
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				baseline = append(baseline, time.Since(start))
			}
			t.Logf("baseline (dbtx.WithUser with SELECT 1): p95=%.3f ms", ms(p95(baseline)))

			// One plan per size, for the same statement and arguments the
			// handler sends, as the member.
			if _, err := searchAs(ctx, t, pool, user, household, searchPerfPlanQ, 10); err != nil {
				t.Fatal(err)
			}
			tracer.mu.Lock()
			sql, args := tracer.sql, tracer.args
			tracer.mu.Unlock()
			var plan []string
			err = dbtx.WithUser(ctx, base, pgUUID(t, user), func(tx pgx.Tx) error {
				rows, err := tx.Query(ctx, "EXPLAIN (ANALYZE, BUFFERS) "+sql, args...)
				if err != nil {
					return err
				}
				plan, err = pgx.CollectRows(rows, pgx.RowTo[string])
				return err
			})
			if err != nil {
				t.Fatalf("EXPLAIN: %v", err)
			}
			t.Logf("EXPLAIN ANALYZE, q=%q:\n%s", searchPerfPlanQ, strings.Join(plan, "\n"))
		})
	}
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

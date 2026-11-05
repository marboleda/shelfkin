package db_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/marboleda/shelfkin/api/db"
	"github.com/marboleda/shelfkin/api/db/dbtest"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
)

// searchCatalogMigration adds the search_catalog function (#17).
const searchCatalogMigration = "20261105100000_search_catalog.sql"

// TestSearchCatalogFunction checks search_catalog is SECURITY DEFINER with a
// fixed empty search_path and custom plans, and that shelfkin_app can
// execute it but PUBLIC cannot.
func TestSearchCatalogFunction(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)

	var (
		secdef, appExec, publicExec bool
		config                      []string
		result                      string
	)
	err := pool.QueryRow(t.Context(), `
		SELECT p.prosecdef, p.proconfig, pg_get_function_result(p.oid),
		       has_function_privilege('shelfkin_app', p.oid, 'EXECUTE'),
		       EXISTS (SELECT 1 FROM aclexplode(p.proacl) a WHERE a.grantee = 0)
		FROM pg_proc p
		WHERE p.oid = 'public.search_catalog(uuid, text, integer)'::regprocedure`).
		Scan(&secdef, &config, &result, &appExec, &publicExec)
	if err != nil {
		t.Fatal(err)
	}
	if !secdef {
		t.Error("search_catalog is not SECURITY DEFINER")
	}
	if want := []string{`search_path=""`, "plan_cache_mode=force_custom_plan", "enable_seqscan=off"}; !reflect.DeepEqual(config, want) {
		t.Errorf("search_catalog config = %q, want %q", config, want)
	}
	if result != "SETOF effective_catalog_items" {
		t.Errorf("search_catalog returns %q, want SETOF effective_catalog_items", result)
	}
	if !appExec {
		t.Error("shelfkin_app cannot execute search_catalog")
	}
	if publicExec {
		t.Error("PUBLIC can execute search_catalog")
	}
}

// TestSearchCatalogMembership checks search_catalog raises no_data_found for
// a household the caller is not in (or that does not exist, or with no
// user), and returns rows for a member.
func TestSearchCatalogMembership(t *testing.T) {
	t.Parallel()
	pool := dbtest.New(t)
	alice := insertUser(t, pool, "alice")
	bob := insertUser(t, pool, "bob")
	home := insertHousehold(t, pool, "Home", alice)
	if err := insertMember(t.Context(), pool, home, alice, "owner"); err != nil {
		t.Fatal(err)
	}
	other := insertHousehold(t, pool, "Other", nil)

	search := func(user, household, q string) (int, error) {
		n := 0
		fn := func(tx pgx.Tx) error {
			rows, err := tx.Query(t.Context(), `SELECT * FROM search_catalog($1, $2, 25)`, household, q)
			if err != nil {
				return err
			}
			for rows.Next() {
				n++
			}
			return rows.Err()
		}
		var err error
		if user == "" {
			err = dbtx.WithoutUser(t.Context(), pool, fn)
		} else {
			err = dbtx.WithUser(t.Context(), pool, pgUUID(t, user), fn)
		}
		return n, err
	}
	wantNotFound := func(name string, err error) {
		t.Helper()
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "P0002" {
			t.Errorf("%s: err = %v, want SQLSTATE P0002", name, err)
		}
	}

	if n, err := search(alice, home, "milk"); err != nil || n == 0 {
		t.Errorf("member searching milk: %d rows, %v; want some rows", n, err)
	}
	if n, err := search(alice, home, ""); err != nil || n != 0 {
		t.Errorf("member searching '': %d rows, %v; want 0 rows", n, err)
	}
	_, err := search(bob, home, "milk")
	wantNotFound("non-member", err)
	_, err = search(bob, home, "")
	wantNotFound("non-member, empty q", err)
	_, err = search(alice, other, "milk")
	wantNotFound("household with no members", err)
	_, err = search(alice, "00000000-0000-7000-8000-000000000000", "milk")
	wantNotFound("missing household", err)
	_, err = search("", home, "milk")
	wantNotFound("no user", err)
}

// TestSearchCatalogUsesTrigramIndex loads the built-in seed plus 10,000
// custom items over 100 households and checks that the query inside
// search_catalog, run as shelfkin_app through dbtx, uses the trigram GIN
// index for a 3-letter and a 6-letter q. auto_explain logs the nested plan
// (with ANALYZE) as a notice. Run with -v to see the plans and timings.
func TestSearchCatalogUsesTrigramIndex(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	_, err := pool.Exec(ctx, `
		INSERT INTO users (firebase_uid, display_name, timezone)
		SELECT 'perf-' || g, 'Perf ' || g, 'UTC' FROM generate_series(1, 100) g;
		INSERT INTO households (name, created_by)
		SELECT 'House ' || u.display_name, u.id FROM users u WHERE u.firebase_uid LIKE 'perf-%';
		INSERT INTO household_members (household_id, user_id, role)
		SELECT h.id, h.created_by, 'owner' FROM households h;
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
		    WHERE g <= 100
		) x;
		ANALYZE;`)
	if err != nil {
		t.Fatalf("loading custom items: %v", err)
	}
	if n := count(t, pool, `SELECT count(*) FROM catalog_items WHERE household_id IS NOT NULL`); n != 10000 {
		t.Fatalf("%d custom items, want 10000", n)
	}
	var household, user string
	if err := pool.QueryRow(ctx, `SELECT id, created_by FROM households ORDER BY id LIMIT 1`).Scan(&household, &user); err != nil {
		t.Fatal(err)
	}

	// A connection of its own that collects notices.
	var (
		mu      sync.Mutex
		notices []string
	)
	cfg := pool.Config().ConnConfig.Copy()
	cfg.OnNotice = func(_ *pgconn.PgConn, n *pgconn.Notice) {
		mu.Lock()
		defer mu.Unlock()
		notices = append(notices, n.Message)
	}
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	for _, s := range []string{
		`LOAD 'auto_explain'`,
		`SET auto_explain.log_min_duration = 0`,
		`SET auto_explain.log_analyze = on`,
		`SET auto_explain.log_buffers = on`,
		`SET auto_explain.log_nested_statements = on`,
		`SET auto_explain.log_format = json`,
		`SET auto_explain.log_level = notice`,
	} {
		if _, err := conn.Exec(ctx, s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}

	for _, q := range []string{"tom", "tomato"} {
		t.Run(q, func(t *testing.T) {
			// Several calls, so a plan cached by plpgsql would show up.
			for range 7 {
				mu.Lock()
				notices = nil
				mu.Unlock()
				var n int
				err := dbtx.WithUser(ctx, conn, pgUUID(t, user), func(tx pgx.Tx) error {
					return tx.QueryRow(ctx, `SELECT count(*) FROM search_catalog($1, $2, 10)`, household, q).Scan(&n)
				})
				if err != nil {
					t.Fatal(err)
				}
				if n == 0 {
					t.Fatalf("search_catalog(%q) returned no rows", q)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			var plan string
			for _, msg := range notices {
				if strings.Contains(msg, `"Relation Name": "catalog_items"`) {
					plan = msg
				}
			}
			if plan == "" {
				t.Fatalf("no auto_explain plan for the query on catalog_items; notices: %q", notices)
			}
			if !strings.Contains(plan, `"Index Name": "catalog_items_lower_name_trgm"`) {
				t.Errorf("the search does not use catalog_items_lower_name_trgm:\n%s", plan)
			}
			t.Logf("plan for q=%q:\n%s", q, plan)
			if i := strings.Index(plan, "{"); i >= 0 {
				var out struct {
					Plan struct {
						Total float64 `json:"Actual Total Time"`
					} `json:"Plan"`
				}
				if json.Unmarshal([]byte(plan[i:]), &out) == nil {
					t.Logf("q=%q: %.3f ms", q, out.Plan.Total)
				}
			}
		})
	}
}

// TestSearchCatalogMigrationDownUp checks the migration's Down removes the
// function and Up restores it.
func TestSearchCatalogMigrationDownUp(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	pool := dbtest.New(t)

	sqlDB := stdlib.OpenDBFromPool(pool)
	defer sqlDB.Close()
	provider, err := db.NewProvider(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	var this, previous int64 = -1, 0
	for _, s := range provider.ListSources() {
		if filepath.Base(s.Path) == searchCatalogMigration {
			this = s.Version
			break
		}
		previous = s.Version
	}
	if this < 0 {
		t.Fatalf("migration %s not found", searchCatalogMigration)
	}
	exists := func() bool {
		return count(t, pool, `SELECT count(*) FROM pg_proc WHERE proname = 'search_catalog'`) > 0
	}
	if _, err := provider.DownTo(ctx, previous); err != nil {
		t.Fatalf("down to %d: %v", previous, err)
	}
	if exists() {
		t.Error("search_catalog still exists after Down")
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("up: %v", err)
	}
	if !exists() {
		t.Error("search_catalog missing after Up")
	}
}

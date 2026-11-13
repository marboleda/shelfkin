package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"

	"github.com/jackc/pgx/v5"
)

// APIRoleEnv names the environment variable holding the login role the API
// server and the expiry job connect as (shelfkin_api in production). When it
// is set, `migrate up` makes sure that login can use shelfkin_app after
// applying migrations. When it is unset (local development, tests), the grant
// step is skipped.
const APIRoleEnv = "API_DB_ROLE"

// appRole is the restricted role created by the RLS migration (#10). Keep in
// sync with api/internal/dbtx.AppRole.
const appRole = "shelfkin_app"

// grantAppRole makes apiRole a member of shelfkin_app with INHERIT FALSE and
// SET TRUE, so the login can use shelfkin_app's privileges only after
// SET ROLE, and has none of them after RESET ROLE. It then removes anything
// else that would let the login do more than shelfkin_app (see #38): other
// role memberships and the CREATEDB, CREATEROLE, BYPASSRLS and REPLICATION
// attributes.
//
// On Postgres 16 and later pg_auth_members holds one row per grantor, and
// GRANT and REVOKE only touch the row whose grantor is the role running them.
// So every membership row is revoked as granted by its own grantor (REVOKE
// ... GRANTED BY), and the final check reads every row again. Changes the
// runner cannot make, such as a membership granted by a Cloud SQL admin, make
// it fail, naming the grantor and the one-time admin step in the README.
//
// It is idempotent: when everything is already in place it changes nothing.
func grantAppRole(ctx context.Context, sqlDB *sql.DB, apiRole string, out io.Writer) error {
	conn, err := sqlDB.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	var exists bool
	if err := conn.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT FROM pg_catalog.pg_roles WHERE rolname = $1)", apiRole).Scan(&exists); err != nil {
		return fmt.Errorf("looking up role %s: %w", apiRole, err)
	}
	if !exists {
		return fmt.Errorf("%s=%s, but no such role exists", APIRoleEnv, apiRole)
	}
	if apiRole == appRole {
		return fmt.Errorf("%s must be the login role the API connects as, not %s", APIRoleEnv, appRole)
	}
	ident := pgx.Identifier{apiRole}.Sanitize()

	// 1. A shelfkin_app membership with the right options. Granting again
	// updates the options of the runner's own row. It is only needed when no
	// row is right yet, so a right row exists before step 2 revokes wrong
	// ones, and the serving API never loses shelfkin_app.
	grants, err := memberGrants(ctx, conn, ident)
	if err != nil {
		return err
	}
	appGrants, _ := splitGrants(grants)
	switch {
	case len(appGrants) == 0:
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("GRANT %s TO %s WITH INHERIT FALSE, SET TRUE", appRole, ident)); err != nil {
			return fmt.Errorf("granting %s to %s: %w", appRole, apiRole, err)
		}
		fmt.Fprintf(out, "Granted %s to %s (INHERIT FALSE, SET TRUE)\n", appRole, apiRole)
	case !hasRightAppGrant(appGrants):
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("GRANT %s TO %s WITH INHERIT FALSE, SET TRUE", appRole, ident)); err != nil {
			return fmt.Errorf("updating the %s grant to %s: %w", appRole, apiRole, err)
		}
		fmt.Fprintf(out, "Updated the %s grant to %s to INHERIT FALSE, SET TRUE\n", appRole, apiRole)
	default:
		fmt.Fprintf(out, "%s already has %s (INHERIT FALSE, SET TRUE)\n", apiRole, appRole)
	}

	// 2. Best effort: revoke every wrong shelfkin_app row (for example an
	// INHERIT TRUE grant made by an admin) and every other membership, each
	// as granted by its own grantor. Revoking a row granted by a role whose
	// privileges the runner does not have fails; whatever stays is reported
	// by checkAPIRole below, so the error is not returned here.
	if grants, err = memberGrants(ctx, conn, ident); err != nil {
		return err
	}
	appGrants, otherGrants := splitGrants(grants)
	var revokes []string
	for _, g := range appGrants {
		grantor := pgx.Identifier{g.grantor}.Sanitize()
		switch {
		case g.right():
		case !g.inherit && g.set:
			// Only ADMIN is wrong: keep the membership, drop the option.
			revokes = append(revokes, fmt.Sprintf("REVOKE ADMIN OPTION FOR %s FROM %s GRANTED BY %s", appRole, ident, grantor))
		default:
			revokes = append(revokes, fmt.Sprintf("REVOKE %s FROM %s GRANTED BY %s", appRole, ident, grantor))
		}
	}
	for _, g := range otherGrants {
		revokes = append(revokes, fmt.Sprintf("REVOKE %s FROM %s GRANTED BY %s",
			pgx.Identifier{g.role}.Sanitize(), ident, pgx.Identifier{g.grantor}.Sanitize()))
	}
	for _, stmt := range revokes {
		if _, err := conn.ExecContext(ctx, stmt); err == nil {
			fmt.Fprintf(out, "Ran %s\n", stmt)
		}
	}

	attrs, err := roleAttributes(ctx, conn, apiRole)
	if err != nil {
		return err
	}
	for _, a := range []struct {
		on   bool
		attr string
	}{{attrs.createDB, "NOCREATEDB"}, {attrs.createRole, "NOCREATEROLE"}, {attrs.bypassRLS, "NOBYPASSRLS"}, {attrs.replication, "NOREPLICATION"}} {
		if !a.on {
			continue
		}
		stmt := fmt.Sprintf("ALTER ROLE %s %s", ident, a.attr)
		if _, err := conn.ExecContext(ctx, stmt); err == nil {
			fmt.Fprintf(out, "Ran %s\n", stmt)
		}
	}

	return checkAPIRole(ctx, conn, apiRole)
}

type attributes struct {
	superuser, createDB, createRole, bypassRLS, replication bool
}

func roleAttributes(ctx context.Context, conn *sql.Conn, role string) (attributes, error) {
	var a attributes
	err := conn.QueryRowContext(ctx,
		"SELECT rolsuper, rolcreatedb, rolcreaterole, rolbypassrls, rolreplication FROM pg_catalog.pg_roles WHERE rolname = $1",
		role).Scan(&a.superuser, &a.createDB, &a.createRole, &a.bypassRLS, &a.replication)
	if err != nil {
		return a, fmt.Errorf("reading attributes of %s: %w", role, err)
	}
	return a, nil
}

// memberGrant is one pg_auth_members row: the member belongs to role, granted
// by grantor with these options.
type memberGrant struct {
	role, grantor       string
	inherit, set, admin bool
}

// right reports whether g has the only shape a shelfkin_app grant may have:
// INHERIT FALSE, SET TRUE and no ADMIN.
func (g memberGrant) right() bool { return !g.inherit && g.set && !g.admin }

func (g memberGrant) String() string {
	return fmt.Sprintf("%s (granted by %s with INHERIT %s, SET %s, ADMIN %s)",
		g.role, g.grantor, boolWord(g.inherit), boolWord(g.set), boolWord(g.admin))
}

func boolWord(b bool) string {
	if b {
		return "TRUE"
	}
	return "FALSE"
}

// memberGrants lists every pg_auth_members row for member ident, one per role
// and grantor.
func memberGrants(ctx context.Context, conn *sql.Conn, ident string) ([]memberGrant, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT r.rolname, pg_catalog.pg_get_userbyid(m.grantor), m.inherit_option, m.set_option, m.admin_option
		FROM pg_catalog.pg_auth_members m
		JOIN pg_catalog.pg_roles r ON r.oid = m.roleid
		WHERE m.member = $1::regrole
		ORDER BY 1, 2`, ident)
	if err != nil {
		return nil, fmt.Errorf("listing role memberships: %w", err)
	}
	defer rows.Close()
	var grants []memberGrant
	for rows.Next() {
		var g memberGrant
		if err := rows.Scan(&g.role, &g.grantor, &g.inherit, &g.set, &g.admin); err != nil {
			return nil, err
		}
		grants = append(grants, g)
	}
	return grants, rows.Err()
}

// splitGrants separates the shelfkin_app rows from every other membership.
func splitGrants(grants []memberGrant) (app, others []memberGrant) {
	for _, g := range grants {
		if g.role == appRole {
			app = append(app, g)
		} else {
			others = append(others, g)
		}
	}
	return app, others
}

func hasRightAppGrant(app []memberGrant) bool {
	for _, g := range app {
		if g.right() {
			return true
		}
	}
	return false
}

// checkAPIRole fails unless apiRole can do no more than shelfkin_app: no
// superuser, CREATEDB, CREATEROLE, BYPASSRLS or REPLICATION, no role membership besides
// shelfkin_app, every shelfkin_app grant (whoever made it) INHERIT FALSE,
// SET TRUE without ADMIN, and no objects or privileges of its own in this
// database.
func checkAPIRole(ctx context.Context, conn *sql.Conn, apiRole string) error {
	ident := pgx.Identifier{apiRole}.Sanitize()
	var problems []string

	attrs, err := roleAttributes(ctx, conn, apiRole)
	if err != nil {
		return err
	}
	for _, a := range []struct {
		on   bool
		attr string
	}{{attrs.superuser, "SUPERUSER"}, {attrs.createDB, "CREATEDB"}, {attrs.createRole, "CREATEROLE"}, {attrs.bypassRLS, "BYPASSRLS"}, {attrs.replication, "REPLICATION"}} {
		if a.on {
			problems = append(problems, "has "+a.attr)
		}
	}

	members, err := memberGrants(ctx, conn, ident)
	if err != nil {
		return err
	}
	appGrants, otherGrants := splitGrants(members)
	if !hasRightAppGrant(appGrants) {
		problems = append(problems, fmt.Sprintf("has no %s grant with INHERIT FALSE, SET TRUE", appRole))
	}
	for _, g := range appGrants {
		if !g.right() {
			problems = append(problems, "has a wrong grant of "+g.String())
		}
	}
	for _, g := range otherGrants {
		problems = append(problems, "is a member of "+g.String())
	}

	deps, err := roleDependencies(ctx, conn, ident)
	if err != nil {
		return fmt.Errorf("checking objects owned by or granted to %s: %w", apiRole, err)
	}
	for _, kind := range []struct{ deptype, phrase string }{
		{"o", "owns %d objects (%s)"},
		{"a", "has privileges granted directly to it on %d objects (%s)"},
		{"r", "is named in %d policies (%s)"},
	} {
		objs := deps[kind.deptype]
		if len(objs) == 0 {
			continue
		}
		shown := objs
		if len(shown) > 10 {
			shown = append(shown[:10:10], "...")
		}
		problems = append(problems, fmt.Sprintf(kind.phrase, len(objs), strings.Join(shown, "; ")))
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s can do more than %s: it %s.\n"+
		"Migrations were applied, but the API must not be deployed like this. "+
		"An admin must fix it once (README, \"Deploying the API\" > \"One-time admin step\"), then run the Deploy API workflow by hand.",
		apiRole, appRole, strings.Join(problems, ", "))
}

// roleDependencies lists, by pg_shdepend deptype, every object that depends
// on the role ident, described with pg_describe_object:
//
//   - "o": objects it owns, of any kind (tables, types and domains, functions,
//     schemas, statistics objects, collations, text search objects, operator
//     families, publications, foreign servers, large objects, databases,
//     tablespaces, ...);
//   - "a": objects whose ACL names it, which is every direct grant, whatever
//     the catalog (single columns, relations, functions, schemas, types, large
//     objects, languages, foreign-data wrappers and servers, parameters,
//     default privileges, tablespaces and databases). A database grant of only
//     CONNECT and TEMPORARY is not counted: every login needs those and PUBLIC
//     has them by default;
//   - "r": policies that name it in TO.
//
// pg_shdepend is shared, so it covers the current database, shared objects
// (dbid 0) and every other database. Objects in another database cannot be
// described from here, so they are named by database only. Grants to PUBLIC
// are not grants to the role and are not listed.
//
// One catalog replaces per-catalog checks (relacl, attacl, typacl, ...),
// which missed whatever kind of object had not been thought of yet.
func roleDependencies(ctx context.Context, conn *sql.Conn, ident string) (map[string][]string, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT sd.deptype::text,
			CASE WHEN sd.dbid = 0 OR sd.dbid = (SELECT oid FROM pg_catalog.pg_database WHERE datname = current_database())
				THEN pg_catalog.pg_describe_object(sd.classid, sd.objid, sd.objsubid)
				ELSE format('an object in database %s', (SELECT datname FROM pg_catalog.pg_database WHERE oid = sd.dbid))
			END
		FROM pg_catalog.pg_shdepend sd
		WHERE sd.refclassid = 'pg_catalog.pg_authid'::regclass
		  AND sd.refobjid = $1::regrole
		  AND sd.deptype IN ('o', 'a', 'r')
		  AND NOT (sd.deptype = 'a' AND sd.classid = 'pg_catalog.pg_database'::regclass AND NOT EXISTS (
			SELECT FROM pg_catalog.pg_database d, aclexplode(d.datacl) a
			WHERE d.oid = sd.objid AND a.grantee = sd.refobjid
			  AND a.privilege_type NOT IN ('CONNECT', 'TEMPORARY')))
		ORDER BY 1, 2`, ident)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	deps := map[string][]string{}
	for rows.Next() {
		var deptype, desc string
		if err := rows.Scan(&deptype, &desc); err != nil {
			return nil, err
		}
		deps[deptype] = append(deps[deptype], desc)
	}
	return deps, rows.Err()
}

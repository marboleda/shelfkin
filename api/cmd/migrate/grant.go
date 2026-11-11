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
// role memberships and the CREATEDB, CREATEROLE and BYPASSRLS attributes.
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
	}{{attrs.createDB, "NOCREATEDB"}, {attrs.createRole, "NOCREATEROLE"}, {attrs.bypassRLS, "NOBYPASSRLS"}} {
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
	superuser, createDB, createRole, bypassRLS bool
}

func roleAttributes(ctx context.Context, conn *sql.Conn, role string) (attributes, error) {
	var a attributes
	err := conn.QueryRowContext(ctx,
		"SELECT rolsuper, rolcreatedb, rolcreaterole, rolbypassrls FROM pg_catalog.pg_roles WHERE rolname = $1",
		role).Scan(&a.superuser, &a.createDB, &a.createRole, &a.bypassRLS)
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
// superuser, CREATEDB, CREATEROLE or BYPASSRLS, no role membership besides
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
	}{{attrs.superuser, "SUPERUSER"}, {attrs.createDB, "CREATEDB"}, {attrs.createRole, "CREATEROLE"}, {attrs.bypassRLS, "BYPASSRLS"}} {
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

	var owned int
	if err := conn.QueryRowContext(ctx, `
		SELECT (SELECT count(*) FROM pg_catalog.pg_class WHERE relowner = $1::regrole)
		     + (SELECT count(*) FROM pg_catalog.pg_proc WHERE proowner = $1::regrole)
		     + (SELECT count(*) FROM pg_catalog.pg_namespace WHERE nspowner = $1::regrole)
		     + (SELECT count(*) FROM pg_catalog.pg_type WHERE typowner = $1::regrole AND typrelid = 0 AND typcategory <> 'A')`,
		ident).Scan(&owned); err != nil {
		return fmt.Errorf("checking objects owned by %s: %w", apiRole, err)
	}
	if owned > 0 {
		problems = append(problems, fmt.Sprintf("owns %d objects in this database", owned))
	}

	// Direct grants on tables, views, sequences, functions and schemas in
	// this database (PUBLIC's grants are not counted: they name grantee 0).
	var grants int
	if err := conn.QueryRowContext(ctx, `
		SELECT count(*) FROM (
			SELECT (aclexplode(relacl)).grantee FROM pg_catalog.pg_class WHERE relacl IS NOT NULL
			UNION ALL
			SELECT (aclexplode(proacl)).grantee FROM pg_catalog.pg_proc WHERE proacl IS NOT NULL
			UNION ALL
			SELECT (aclexplode(nspacl)).grantee FROM pg_catalog.pg_namespace WHERE nspacl IS NOT NULL
		) g
		WHERE g.grantee = $1::regrole`,
		ident).Scan(&grants); err != nil {
		return fmt.Errorf("checking privileges granted to %s: %w", apiRole, err)
	}
	if grants > 0 {
		problems = append(problems, fmt.Sprintf("has %d privileges granted directly to it", grants))
	}

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("%s can do more than %s: it %s.\n"+
		"Migrations were applied, but the API must not be deployed like this. "+
		"An admin must fix it once (README, \"Deploying the API\" > \"One-time admin step\"), then run the Deploy API workflow by hand.",
		apiRole, appRole, strings.Join(problems, ", "))
}

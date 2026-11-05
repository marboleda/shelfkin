package main

import (
	"context"
	"database/sql"
	"errors"
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
// Changes it cannot make (for example a membership granted by a Cloud SQL
// admin) make it fail, naming the one-time admin step in the README.
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

	// 1. The shelfkin_app membership, with the right options.
	var granted, inherit, set bool
	err = conn.QueryRowContext(ctx, `
		SELECT true, bool_or(m.inherit_option), bool_or(m.set_option)
		FROM pg_catalog.pg_auth_members m
		WHERE m.roleid = $1::regrole AND m.member = $2::regrole
		GROUP BY m.member`, appRole, ident).Scan(&granted, &inherit, &set)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("checking %s membership of %s: %w", apiRole, appRole, err)
	}
	switch {
	case !granted:
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("GRANT %s TO %s WITH INHERIT FALSE, SET TRUE", appRole, ident)); err != nil {
			return fmt.Errorf("granting %s to %s: %w", appRole, apiRole, err)
		}
		fmt.Fprintf(out, "Granted %s to %s (INHERIT FALSE, SET TRUE)\n", appRole, apiRole)
	case inherit || !set:
		// Granting again updates the options of the existing membership.
		if _, err := conn.ExecContext(ctx, fmt.Sprintf("GRANT %s TO %s WITH INHERIT FALSE, SET TRUE", appRole, ident)); err != nil {
			return fmt.Errorf("updating the %s grant to %s: %w", appRole, apiRole, err)
		}
		fmt.Fprintf(out, "Updated the %s grant to %s to INHERIT FALSE, SET TRUE\n", appRole, apiRole)
	default:
		fmt.Fprintf(out, "%s already has %s (INHERIT FALSE, SET TRUE)\n", apiRole, appRole)
	}

	// 2. Best effort: drop other memberships and attributes. Any that stay
	// are reported by checkAPIRole below. REVOKE of a membership someone else
	// granted only warns, so the result is checked rather than the error.
	others, err := otherMemberships(ctx, conn, ident)
	if err != nil {
		return err
	}
	for _, role := range others {
		stmt := fmt.Sprintf("REVOKE %s FROM %s", pgx.Identifier{role}.Sanitize(), ident)
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

// otherMemberships lists the roles ident is a direct member of, other than
// shelfkin_app.
func otherMemberships(ctx context.Context, conn *sql.Conn, ident string) ([]string, error) {
	rows, err := conn.QueryContext(ctx, `
		SELECT DISTINCT r.rolname
		FROM pg_catalog.pg_auth_members m
		JOIN pg_catalog.pg_roles r ON r.oid = m.roleid
		WHERE m.member = $1::regrole AND r.rolname <> $2
		ORDER BY r.rolname`, ident, appRole)
	if err != nil {
		return nil, fmt.Errorf("listing role memberships: %w", err)
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var r string
		if err := rows.Scan(&r); err != nil {
			return nil, err
		}
		roles = append(roles, r)
	}
	return roles, rows.Err()
}

// checkAPIRole fails unless apiRole can do no more than shelfkin_app: no
// superuser, CREATEDB, CREATEROLE or BYPASSRLS, no role membership besides
// shelfkin_app, and no objects or privileges of its own in this database.
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

	others, err := otherMemberships(ctx, conn, ident)
	if err != nil {
		return err
	}
	for _, r := range others {
		problems = append(problems, "is a member of "+r)
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

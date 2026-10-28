// Package db holds Shelfkin's database migrations and the helpers that run
// them. The migrations are embedded in the binary, so they can be applied from
// any working directory without a separate goose install.
package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"

	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var embedded embed.FS

// Migrations returns the embedded goose SQL migrations, rooted at the
// migrations directory.
func Migrations() fs.FS {
	sub, err := fs.Sub(embedded, "migrations")
	if err != nil {
		// fs.Sub only fails for an invalid path, which is a compile-time constant here.
		panic(err)
	}
	return sub
}

// NewProvider returns a goose provider that runs the embedded migrations
// against sqlDB, which must be a Postgres database.
func NewProvider(sqlDB *sql.DB) (*goose.Provider, error) {
	return goose.NewProvider(goose.DialectPostgres, sqlDB, Migrations(),
		goose.WithDisableGlobalRegistry(true),
	)
}

// PortEnv names the environment variable that overrides the host port of the
// local compose database.
const PortEnv = "SHELFKIN_DB_PORT"

// LocalURL returns the connection string for the local database started by
// compose.yaml. It honours SHELFKIN_DB_PORT, defaulting to 5432. The
// credentials are local-development-only values.
func LocalURL() string {
	port := os.Getenv(PortEnv)
	if port == "" {
		port = "5432"
	}
	return fmt.Sprintf("postgres://shelfkin:shelfkin@localhost:%s/shelfkin?sslmode=disable", port)
}

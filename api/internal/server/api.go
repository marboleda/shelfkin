package server

import (
	"context"
	"log/slog"
	"time"

	"github.com/marboleda/shelfkin/api/internal/db"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// HealthTimeout bounds how long GET /health waits for the database before
// reporting it unavailable.
const HealthTimeout = 2 * time.Second

// CodeDatabaseUnavailable is the error code GET /health returns when the
// database does not answer.
const CodeDatabaseUnavailable = "database_unavailable"

// API implements the endpoints of the OpenAPI spec.
type API struct {
	queries *db.Queries
	// db opens the transactions of authenticated endpoints, through
	// dbtx.WithUser.
	db     dbtx.Beginner
	logger *slog.Logger
}

// Pool is the database connection an API needs: plain queries (GET /health)
// and transactions (everything else). *pgxpool.Pool satisfies it.
type Pool interface {
	db.DBTX
	dbtx.Beginner
}

var _ openapi.StrictServerInterface = (*API)(nil)

// NewAPI returns the API backed by pool, normally a *pgxpool.Pool.
func NewAPI(pool Pool, logger *slog.Logger) *API {
	return &API{queries: db.New(pool), db: pool, logger: logger}
}

// GetHealth reports whether the database answers a query within
// HealthTimeout.
func (a *API) GetHealth(ctx context.Context, _ openapi.GetHealthRequestObject) (openapi.GetHealthResponseObject, error) {
	ctx, cancel := context.WithTimeout(ctx, HealthTimeout)
	defer cancel()
	if _, err := a.queries.Ping(ctx); err != nil {
		a.logger.WarnContext(ctx, "health check: database unavailable", "error", err)
		return openapi.GetHealth503JSONResponse{Error: openapi.ErrorDetail{
			Code:    CodeDatabaseUnavailable,
			Message: "The database is unavailable.",
		}}, nil
	}
	return openapi.GetHealth200JSONResponse{Status: openapi.HealthStatusOk}, nil
}

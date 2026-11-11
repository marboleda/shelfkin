package server

import (
	"context"
	"log/slog"
	"strings"
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
	// appBaseURL is the base of invite links, without a trailing slash.
	appBaseURL string
	// newInviteCode generates invite codes; tests replace it.
	newInviteCode func() (string, error)
}

// DefaultAppBaseURL is the base of invite links when NewAPI is given no
// WithAppBaseURL option: the local Expo web dev server.
const DefaultAppBaseURL = "http://localhost:8081"

// Option configures an API.
type Option func(*API)

// WithAppBaseURL sets the base URL of invite links, <base>/join/<code>
// (APP_BASE_URL). A trailing slash is ignored.
func WithAppBaseURL(base string) Option {
	return func(a *API) { a.appBaseURL = strings.TrimRight(base, "/") }
}

// Pool is the database connection an API needs: plain queries (GET /health)
// and transactions (everything else). *pgxpool.Pool satisfies it.
type Pool interface {
	db.DBTX
	dbtx.Beginner
}

var _ openapi.StrictServerInterface = (*API)(nil)

// NewAPI returns the API backed by pool, normally a *pgxpool.Pool, configured
// by opts.
func NewAPI(pool Pool, logger *slog.Logger, opts ...Option) *API {
	a := &API{
		queries:       db.New(pool),
		db:            pool,
		logger:        logger,
		appBaseURL:    DefaultAppBaseURL,
		newInviteCode: NewInviteCode,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
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

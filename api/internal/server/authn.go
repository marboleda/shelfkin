package server

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// CodeUnauthorized is the error code of a request with a missing or invalid
// bearer token.
const CodeUnauthorized = "unauthorized"

// unauthorizedMessage never includes the token or why it was rejected.
const unauthorizedMessage = "A valid Firebase ID token is required."

// PublicRoutes are the routes, as ServeMux patterns, that need no
// Authorization header: the operations with `security: []` in
// openapi/openapi.yaml. Every other route requires a valid token.
var PublicRoutes = map[string]bool{
	http.MethodGet + " /health": true,
}

// Authenticator turns a bearer token into the caller's Shelfkin user ID.
// *auth.Authenticator implements it. Errors wrapping auth.ErrInvalidToken
// mean 401; any other error means 500.
type Authenticator interface {
	Authenticate(ctx context.Context, idToken string) (pgtype.UUID, error)
}

// requireAuth returns route middleware that lets requests to PublicRoutes
// through and, for every other route, verifies the bearer token, stores the
// caller's user ID in the request context (auth.WithUserID) and answers 401
// in the Error format if there is no valid token. It runs before the request
// body is read.
func requireAuth(authn Authenticator, logger *slog.Logger) openapi.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// r.Pattern is the ServeMux pattern the request matched, set
			// before route middleware runs.
			if PublicRoutes[r.Pattern] {
				next.ServeHTTP(w, r)
				return
			}
			token, ok := bearerToken(r.Header.Get("Authorization"))
			if !ok {
				writeError(w, http.StatusUnauthorized, CodeUnauthorized, unauthorizedMessage)
				return
			}
			userID, err := authn.Authenticate(r.Context(), token)
			if errors.Is(err, auth.ErrInvalidToken) {
				// The reason comes from the verifier and never contains the
				// token itself.
				logger.DebugContext(r.Context(), "rejected ID token", "path", r.URL.Path, "reason", err.Error())
				writeError(w, http.StatusUnauthorized, CodeUnauthorized, unauthorizedMessage)
				return
			}
			if err != nil {
				logger.ErrorContext(r.Context(), "authenticating request",
					"method", r.Method, "path", r.URL.Path, "error", err)
				writeInternal(w)
				return
			}
			next.ServeHTTP(w, r.WithContext(auth.WithUserID(r.Context(), userID)))
		})
	}
}

// bearerToken extracts the token from an Authorization header of the form
// "Bearer <token>". The scheme is case-insensitive (RFC 9110); the token
// must be non-empty and contain no spaces.
func bearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimLeft(token, " ")
	if token == "" || strings.ContainsAny(token, " \t") {
		return "", false
	}
	return token, true
}

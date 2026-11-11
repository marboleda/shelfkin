package server

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/db"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// Error codes of POST /me/push-tokens.
const (
	CodeInvalidToken    = "invalid_token"
	CodeInvalidPlatform = "invalid_platform"
)

const sqlStateForeignKeyViolation = "23503"

// MaxPushTokenLength is the longest push token the API accepts, in bytes
// (tokens are ASCII).
const MaxPushTokenLength = 255

// expoPushTokenPrefixes are the forms of Expo push token the API accepts:
// ExponentPushToken[...] and ExpoPushToken[...].
var expoPushTokenPrefixes = []string{"ExponentPushToken[", "ExpoPushToken["}

// ValidPushToken reports whether token looks like an Expo push token:
// ExponentPushToken[...] or ExpoPushToken[...], at most MaxPushTokenLength
// characters, with a non-empty value between the brackets made of printable
// ASCII other than "[", "]" and "/". Excluding "/" keeps the token a single
// path segment in DELETE /me/push-tokens/{token}.
func ValidPushToken(token string) bool {
	if len(token) > MaxPushTokenLength || !strings.HasSuffix(token, "]") {
		return false
	}
	for _, prefix := range expoPushTokenPrefixes {
		if !strings.HasPrefix(token, prefix) {
			continue
		}
		value := token[len(prefix) : len(token)-1]
		if value == "" {
			return false
		}
		for i := 0; i < len(value); i++ {
			c := value[i]
			if c <= ' ' || c > '~' || c == '[' || c == ']' || c == '/' {
				return false
			}
		}
		return true
	}
	return false
}

// ValidPushPlatform reports whether p is one of the PushPlatform values.
func ValidPushPlatform(p openapi.PushPlatform) bool {
	switch p {
	case openapi.PushPlatformIos, openapi.PushPlatformAndroid, openapi.PushPlatformWeb:
		return true
	}
	return false
}

// RegisterPushToken registers or refreshes the caller's push token, moving it
// from another user if needed.
func (a *API) RegisterPushToken(ctx context.Context, req openapi.RegisterPushTokenRequestObject) (openapi.RegisterPushTokenResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	if req.Body == nil {
		return openapi.RegisterPushToken400JSONResponse(errorBody(CodeBadRequest, "A JSON body is required.")), nil
	}
	if !ValidPushToken(req.Body.Token) {
		return openapi.RegisterPushToken400JSONResponse(errorBody(CodeInvalidToken,
			fmt.Sprintf("token must be an Expo push token such as ExponentPushToken[xxxxxxxxxxxxxxxxxxxxxx], at most %d characters.", MaxPushTokenLength))), nil
	}
	if !ValidPushPlatform(req.Body.Platform) {
		return openapi.RegisterPushToken400JSONResponse(errorBody(CodeInvalidPlatform,
			"platform must be ios, android or web.")), nil
	}

	var row db.UpsertPushTokenRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		var err error
		row, err = db.New(tx).UpsertPushToken(ctx, db.UpsertPushTokenParams{
			UserID:   userID,
			Token:    req.Body.Token,
			Platform: pgtype.Text{String: string(req.Body.Platform), Valid: true},
		})
		return err
	})
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == sqlStateForeignKeyViolation {
			// The user was deleted after the middleware resolved it.
			return openapi.RegisterPushToken401JSONResponse{UnauthorizedJSONResponse: unauthorizedBody()}, nil
		}
		return nil, fmt.Errorf("POST /me/push-tokens: %w", err)
	}
	return openapi.RegisterPushToken200JSONResponse{
		Token:      row.Token,
		Platform:   openapi.PushPlatform(row.Platform.String),
		LastSeenAt: row.LastSeenAt.Time.UTC(),
	}, nil
}

// DeletePushToken deletes the caller's push token. Unknown tokens and other
// users' tokens are left alone and also return 204.
func (a *API) DeletePushToken(ctx context.Context, req openapi.DeletePushTokenRequestObject) (openapi.DeletePushTokenResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		return db.New(tx).DeleteUserPushTokenByValue(ctx, db.DeleteUserPushTokenByValueParams{UserID: userID, Token: req.Token})
	})
	if err != nil {
		return nil, fmt.Errorf("DELETE /me/push-tokens/{token}: %w", err)
	}
	return openapi.DeletePushToken204Response{}, nil
}

package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oapi-codegen/runtime/types"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/db"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
	"github.com/marboleda/shelfkin/api/internal/openapi"

	// Embed the IANA time zone database so time.LoadLocation validates
	// timezone names even in a minimal container image with no zoneinfo.
	_ "time/tzdata"
)

// Error codes of PATCH /me.
const (
	CodeInvalidDisplayName = "invalid_display_name"
	CodeInvalidTimezone    = "invalid_timezone"
)

// errNoUserInContext means a handler behind the auth middleware found no
// user ID in its context, which is a bug.
var errNoUserInContext = errors.New("no authenticated user in the request context")

// GetMe returns the caller's profile.
func (a *API) GetMe(ctx context.Context, _ openapi.GetMeRequestObject) (openapi.GetMeResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	var row db.GetUserRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		var err error
		row, err = db.New(tx).GetUser(ctx, userID)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		// Deleted after the middleware resolved it.
		return openapi.GetMe401JSONResponse{UnauthorizedJSONResponse: unauthorizedBody()}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GET /me: %w", err)
	}
	return openapi.GetMe200JSONResponse(userResponse(row)), nil
}

// PatchMe updates the caller's display name, time zone and/or push opt-in.
func (a *API) PatchMe(ctx context.Context, req openapi.PatchMeRequestObject) (openapi.PatchMeResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	var params db.UpdateUserProfileParams
	params.ID = userID
	if req.Body != nil {
		if req.Body.DisplayName != nil {
			name, ok := ValidateDisplayName(*req.Body.DisplayName)
			if !ok {
				return openapi.PatchMe400JSONResponse(errorBody(CodeInvalidDisplayName,
					fmt.Sprintf("displayName must be 1 to %d characters after trimming spaces.", auth.MaxDisplayNameLength))), nil
			}
			params.DisplayName = pgtype.Text{String: name, Valid: true}
		}
		if req.Body.Timezone != nil {
			if !ValidTimezone(*req.Body.Timezone) {
				return openapi.PatchMe400JSONResponse(errorBody(CodeInvalidTimezone,
					"timezone must be an IANA time zone name such as America/Toronto.")), nil
			}
			params.Timezone = pgtype.Text{String: *req.Body.Timezone, Valid: true}
		}
		if req.Body.PushEnabled != nil {
			params.PushEnabled = pgtype.Bool{Bool: *req.Body.PushEnabled, Valid: true}
		}
	}

	var row db.GetUserRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if !params.DisplayName.Valid && !params.Timezone.Valid && !params.PushEnabled.Valid {
			// Nothing to change: an empty body or {}.
			var err error
			row, err = q.GetUser(ctx, userID)
			return err
		}
		updated, err := q.UpdateUserProfile(ctx, params)
		row = db.GetUserRow(updated)
		return err
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return openapi.PatchMe401JSONResponse{UnauthorizedJSONResponse: unauthorizedBody()}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("PATCH /me: %w", err)
	}
	return openapi.PatchMe200JSONResponse(userResponse(row)), nil
}

// ValidateDisplayName trims name and reports whether the result is 1 to
// auth.MaxDisplayNameLength characters long.
func ValidateDisplayName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	n := utf8.RuneCountInString(name)
	return name, n >= 1 && n <= auth.MaxDisplayNameLength
}

// ValidTimezone reports whether tz is an IANA time zone name that
// time.LoadLocation accepts. "" and "Local" are rejected: LoadLocation maps
// them to UTC and the server's own zone, not to a real IANA zone.
func ValidTimezone(tz string) bool {
	if tz == "" || tz == "Local" {
		return false
	}
	_, err := time.LoadLocation(tz)
	return err == nil
}

func userResponse(row db.GetUserRow) openapi.User {
	u := openapi.User{
		Id:          types.UUID(row.ID.Bytes),
		DisplayName: row.DisplayName,
		Timezone:    row.Timezone,
		PushEnabled: row.PushEnabled,
	}
	if row.Email.Valid {
		email := row.Email.String
		u.Email = &email
	}
	return u
}

func errorBody(code, message string) openapi.Error {
	return openapi.Error{Error: openapi.ErrorDetail{Code: code, Message: message}}
}

func unauthorizedBody() openapi.UnauthorizedJSONResponse {
	return openapi.UnauthorizedJSONResponse(errorBody(CodeUnauthorized, unauthorizedMessage))
}

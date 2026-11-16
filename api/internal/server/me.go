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

// Error codes of DELETE /me.
const (
	CodeMustTransferOwnership = "must_transfer_ownership"
	CodeAccountDeletionFailed = "account_deletion_failed"
)

// AccountDeletionTimeout bounds the call that deletes the Firebase user in
// DELETE /me, which runs while the database transaction is open.
const AccountDeletionTimeout = 10 * time.Second

const accountDeletionFailedMessage = "Your account could not be deleted right now. Nothing was deleted; try again later."

// errAccountDeletion marks a failure to delete the Firebase user, which
// DELETE /me answers with 503.
var errAccountDeletion = errors.New("deleting the Firebase user failed")

// DeleteMe deletes the caller's account: the households they own alone, their
// users row (which cascades to memberships and push tokens and clears their
// name from the history of other households) and their Firebase user, all or
// nothing. See the delete_account migration.
func (a *API) DeleteMe(ctx context.Context, _ openapi.DeleteMeRequestObject) (openapi.DeleteMeResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	var blocking []db.DeleteAccountRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		uid, err := q.GetUserFirebaseUID(ctx, userID)
		if err != nil {
			return err
		}
		blocking, err = q.DeleteAccount(ctx)
		if err != nil {
			return fmt.Errorf("delete_account: %w", err)
		}
		if len(blocking) > 0 {
			// Nothing was deleted; the rollback is a no-op.
			return errMustTransfer
		}
		// The Firebase user goes last, while the transaction is still open:
		// if it fails, the rollback undoes the database deletion and the
		// caller can retry. If the commit fails after it succeeded, a retry
		// with the same token still works (a missing Firebase user counts as
		// deleted).
		if a.accountDeleter == nil {
			return fmt.Errorf("%w: account deletion is not configured", errAccountDeletion)
		}
		fbCtx, cancel := context.WithTimeout(ctx, AccountDeletionTimeout)
		defer cancel()
		if err := a.accountDeleter.DeleteUser(fbCtx, uid); err != nil {
			return fmt.Errorf("%w: %w", errAccountDeletion, err)
		}
		return nil
	})
	switch {
	case err == nil:
		return openapi.DeleteMe204Response{}, nil
	case errors.Is(err, errMustTransfer):
		return deleteMeConflict(blocking), nil
	case errors.Is(err, pgx.ErrNoRows):
		// Deleted after the middleware resolved it.
		return openapi.DeleteMe401JSONResponse{UnauthorizedJSONResponse: unauthorizedBody()}, nil
	case errors.Is(err, errAccountDeletion):
		a.logger.ErrorContext(ctx, "DELETE /me: nothing was deleted", "error", err)
		return openapi.DeleteMe503JSONResponse(errorBody(CodeAccountDeletionFailed, accountDeletionFailedMessage)), nil
	default:
		return nil, fmt.Errorf("DELETE /me: %w", err)
	}
}

// errMustTransfer rolls back DELETE /me when the caller owns households that
// have other members.
var errMustTransfer = errors.New("the caller owns households with other members")

func deleteMeConflict(rows []db.DeleteAccountRow) openapi.DeleteMe409JSONResponse {
	var body openapi.DeleteAccountConflict
	body.Error.Code = CodeMustTransferOwnership
	body.Error.Households = make([]openapi.HouseholdRef, len(rows))
	names := make([]string, len(rows))
	for i, r := range rows {
		body.Error.Households[i] = openapi.HouseholdRef{Id: types.UUID(r.HouseholdID.Bytes), Name: r.HouseholdName}
		names[i] = `"` + r.HouseholdName + `"`
	}
	body.Error.Message = fmt.Sprintf("Transfer ownership of %s to another member before deleting your account.", joinNames(names))
	return openapi.DeleteMe409JSONResponse(body)
}

// joinNames joins names as "a", "a and b" or "a, b and c".
func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

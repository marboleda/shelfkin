package server

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/runtime/types"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/db"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// Error codes of the invites endpoints.
const (
	CodeInviteNotFound = "invite_not_found"
	CodeInviteRevoked  = "invite_revoked"
	CodeInviteExpired  = "invite_expired"
)

// InviteCodeAlphabet is the 31 characters invite codes are made of: A to Z
// and 2 to 9 without the easily confused I, L, O, 0 and 1.
const InviteCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// InviteCodeLength is the number of characters in an invite code.
const InviteCodeLength = 8

// maxInviteCodeAttempts bounds how many codes CreateInvite tries before
// giving up when every one is already taken.
const maxInviteCodeAttempts = 5

// inviteCodePattern matches a normalised invite code.
var inviteCodePattern = regexp.MustCompile(`^[A-HJKMNP-Z2-9]{8}$`)

// Statuses returned by the join_household function.
const (
	joinStatusJoined        = "joined"
	joinStatusAlreadyMember = "already_member"
	joinStatusNotFound      = "not_found"
	joinStatusRevoked       = "revoked"
	joinStatusExpired       = "expired"
)

// Errors that end an invites transaction early; the handlers turn them into
// responses.
var (
	errInviteNotFound       = errors.New("invite not found")
	errInviteRevoked        = errors.New("invite revoked")
	errInviteExpired        = errors.New("invite expired")
	errInviteCodesExhausted = fmt.Errorf("every one of %d generated invite codes was already taken", maxInviteCodeAttempts)
)

// NewInviteCode returns a random invite code: InviteCodeLength characters
// drawn uniformly from InviteCodeAlphabet with crypto/rand.
func NewInviteCode() (string, error) {
	const n = len(InviteCodeAlphabet)
	// Rejection sampling: bytes at or above the largest multiple of n are
	// thrown away, so every character is equally likely.
	const limit = 256 - 256%n
	code := make([]byte, 0, InviteCodeLength)
	var buf [16]byte
	for len(code) < InviteCodeLength {
		if _, err := rand.Read(buf[:]); err != nil {
			return "", fmt.Errorf("reading random bytes: %w", err)
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			code = append(code, InviteCodeAlphabet[int(b)%n])
			if len(code) == InviteCodeLength {
				break
			}
		}
	}
	return string(code), nil
}

// NormalizeInviteCode removes all whitespace and dashes from code and
// upper-cases its letters. It makes no other substitution.
func NormalizeInviteCode(code string) string {
	var b strings.Builder
	for _, r := range code {
		if r == '-' || unicode.IsSpace(r) {
			continue
		}
		b.WriteRune(unicode.ToUpper(r))
	}
	return b.String()
}

// ValidInviteCode reports whether a normalised code has the format of an
// invite code.
func ValidInviteCode(code string) bool {
	return inviteCodePattern.MatchString(code)
}

// CreateInvite creates an invite code for the household. Owner only.
func (a *API) CreateInvite(ctx context.Context, req openapi.CreateInviteRequestObject) (openapi.CreateInviteResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	var row db.CreateInviteRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := requireOwner(ctx, q, req.HouseholdId, userID); err != nil {
			return err
		}
		for range maxInviteCodeAttempts {
			code, err := a.newInviteCode()
			if err != nil {
				return err
			}
			row, err = q.CreateInvite(ctx, db.CreateInviteParams{
				HouseholdID: pgUUID(req.HouseholdId),
				Code:        code,
				CreatedBy:   userID,
			})
			if errors.Is(err, pgx.ErrNoRows) {
				// The code is taken (ON CONFLICT DO NOTHING); try another.
				continue
			}
			return err
		}
		return errInviteCodesExhausted
	})
	switch {
	case errors.Is(err, errHouseholdNotFound):
		return openapi.CreateInvite404JSONResponse{HouseholdNotFoundJSONResponse: householdNotFoundBody()}, nil
	case errors.Is(err, errOwnerOnly):
		return openapi.CreateInvite403JSONResponse{OwnerOnlyJSONResponse: ownerOnlyBody()}, nil
	case err != nil:
		return nil, fmt.Errorf("POST /households/{id}/invites: %w", err)
	}
	return openapi.CreateInvite201JSONResponse(a.inviteResponse(row.ID.Bytes, row.Code, row.CreatedAt.Time, row.ExpiresAt.Time)), nil
}

// ListInvites returns the household's active invites, newest first. Owner
// only.
func (a *API) ListInvites(ctx context.Context, req openapi.ListInvitesRequestObject) (openapi.ListInvitesResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	var rows []db.ListActiveInvitesRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := requireOwner(ctx, q, req.HouseholdId, userID); err != nil {
			return err
		}
		var err error
		rows, err = q.ListActiveInvites(ctx, pgUUID(req.HouseholdId))
		return err
	})
	switch {
	case errors.Is(err, errHouseholdNotFound):
		return openapi.ListInvites404JSONResponse{HouseholdNotFoundJSONResponse: householdNotFoundBody()}, nil
	case errors.Is(err, errOwnerOnly):
		return openapi.ListInvites403JSONResponse{OwnerOnlyJSONResponse: ownerOnlyBody()}, nil
	case err != nil:
		return nil, fmt.Errorf("GET /households/{id}/invites: %w", err)
	}
	list := openapi.InviteList{Invites: make([]openapi.Invite, 0, len(rows))}
	for _, r := range rows {
		list.Invites = append(list.Invites, a.inviteResponse(r.ID.Bytes, r.Code, r.CreatedAt.Time, r.ExpiresAt.Time))
	}
	return openapi.ListInvites200JSONResponse(list), nil
}

// RevokeInvite revokes one of the household's invites. Owner only.
func (a *API) RevokeInvite(ctx context.Context, req openapi.RevokeInviteRequestObject) (openapi.RevokeInviteResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := requireOwner(ctx, q, req.HouseholdId, userID); err != nil {
			return err
		}
		n, err := q.RevokeInvite(ctx, db.RevokeInviteParams{ID: pgUUID(req.InviteId), HouseholdID: pgUUID(req.HouseholdId)})
		if err != nil {
			return err
		}
		if n == 0 {
			// No such invite in this household, or the caller stopped being
			// the owner since the check (the policy then hides every row).
			return errInviteNotFound
		}
		return nil
	})
	switch {
	case errors.Is(err, errHouseholdNotFound):
		return openapi.RevokeInvite404JSONResponse(householdNotFoundBody()), nil
	case errors.Is(err, errInviteNotFound):
		return openapi.RevokeInvite404JSONResponse(errorBody(CodeNotFound, "Invite not found.")), nil
	case errors.Is(err, errOwnerOnly):
		return openapi.RevokeInvite403JSONResponse{OwnerOnlyJSONResponse: ownerOnlyBody()}, nil
	case err != nil:
		return nil, fmt.Errorf("DELETE /households/{id}/invites/{inviteId}: %w", err)
	}
	return openapi.RevokeInvite204Response{}, nil
}

// JoinInvite adds the caller to the household of an invite code as a
// member. The code is never logged: errors returned from here do not
// include it.
func (a *API) JoinInvite(ctx context.Context, req openapi.JoinInviteRequestObject) (openapi.JoinInviteResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	if req.Body == nil {
		return openapi.JoinInvite400JSONResponse{BadRequestJSONResponse: openapi.BadRequestJSONResponse(
			errorBody(CodeBadRequest, "The request body is required."))}, nil
	}
	code := NormalizeInviteCode(req.Body.Code)
	if code == "" {
		return openapi.JoinInvite400JSONResponse{BadRequestJSONResponse: openapi.BadRequestJSONResponse(
			errorBody(CodeBadRequest, "code is required."))}, nil
	}
	if !ValidInviteCode(code) {
		return openapi.JoinInvite404JSONResponse(inviteNotFoundBody()), nil
	}
	var (
		row           db.GetHouseholdRow
		alreadyMember bool
	)
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		res, err := q.JoinHousehold(ctx, code)
		if err != nil {
			return err
		}
		switch res.Status {
		case joinStatusJoined:
		case joinStatusAlreadyMember:
			alreadyMember = true
		case joinStatusNotFound:
			return errInviteNotFound
		case joinStatusRevoked:
			return errInviteRevoked
		case joinStatusExpired:
			return errInviteExpired
		default:
			return fmt.Errorf("join_household returned unknown status %q", res.Status)
		}
		// The caller is a member now, so row-level security lets them read
		// the household. join_household locked it, so it still exists.
		row, err = getMembership(ctx, q, types.UUID(res.HouseholdID.Bytes), userID)
		return err
	})
	switch {
	case errors.Is(err, errInviteNotFound), errors.Is(err, errHouseholdNotFound):
		return openapi.JoinInvite404JSONResponse(inviteNotFoundBody()), nil
	case errors.Is(err, errInviteRevoked):
		return openapi.JoinInvite410JSONResponse(errorBody(CodeInviteRevoked, "This invite code has been revoked.")), nil
	case errors.Is(err, errInviteExpired):
		return openapi.JoinInvite410JSONResponse(errorBody(CodeInviteExpired, "This invite code has expired.")), nil
	case err != nil:
		return nil, fmt.Errorf("POST /invites/join: %w", err)
	}
	return openapi.JoinInvite200JSONResponse(openapi.JoinInviteResult{
		Household:     householdResponse(row.ID, row.Name, row.Role, row.CreatedAt),
		AlreadyMember: alreadyMember,
	}), nil
}

// inviteResponse builds an Invite with its link under the app's base URL.
func (a *API) inviteResponse(id [16]byte, code string, createdAt, expiresAt time.Time) openapi.Invite {
	return openapi.Invite{
		Id:        types.UUID(id),
		Code:      code,
		Url:       a.appBaseURL + "/join/" + code,
		CreatedAt: createdAt.UTC().Truncate(time.Microsecond),
		ExpiresAt: expiresAt.UTC().Truncate(time.Microsecond),
	}
}

func inviteNotFoundBody() openapi.Error {
	return errorBody(CodeInviteNotFound, "This invite code is not valid.")
}

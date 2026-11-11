package server

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oapi-codegen/runtime/types"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/db"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// Error codes of the members endpoints.
const (
	CodeOwnerCannotLeave = "owner_cannot_leave"
	CodeNotAMember       = "not_a_member"
	CodeAlreadyOwner     = "already_owner"
	CodeConflict         = "conflict"
)

// Errors that end a members transaction early; the handlers turn them into
// responses.
var (
	errMemberNotFound   = errors.New("member not found")
	errOwnerCannotLeave = errors.New("owner cannot leave")
	errNotAMember       = errors.New("new owner is not a member")
	errAlreadyOwner     = errors.New("caller is already the owner")
	errConflict         = errors.New("membership changed concurrently")
)

// SQLSTATEs raised by transfer_ownership (see its migration) and by
// concurrent writes.
const (
	sqlStateInsufficientPrivilege = "42501"
	sqlStateInvalidParameterValue = "22023"
	sqlStateNoDataFound           = "P0002"
	sqlStateUniqueViolation       = "23505"
	sqlStateSerializationFailure  = "40001"
	sqlStateDeadlockDetected      = "40P01"
)

// ListMembers returns the household's members, owner first.
func (a *API) ListMembers(ctx context.Context, req openapi.ListMembersRequestObject) (openapi.ListMembersResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	var rows []db.ListMembersRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if _, err := getMembership(ctx, q, req.HouseholdId, userID); err != nil {
			return err
		}
		var err error
		rows, err = q.ListMembers(ctx, pgUUID(req.HouseholdId))
		return err
	})
	if errors.Is(err, errHouseholdNotFound) {
		return openapi.ListMembers404JSONResponse{HouseholdNotFoundJSONResponse: householdNotFoundBody()}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GET /households/{id}/members: %w", err)
	}
	return openapi.ListMembers200JSONResponse(memberListResponse(rows)), nil
}

// RemoveMember removes a member (owner only) or lets the caller leave.
func (a *API) RemoveMember(ctx context.Context, req openapi.RemoveMemberRequestObject) (openapi.RemoveMemberResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	target := pgUUID(req.UserId)
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := checkRemove(ctx, q, req.HouseholdId, userID, target); err != nil {
			return err
		}
		n, err := q.DeleteMember(ctx, db.DeleteMemberParams{HouseholdID: pgUUID(req.HouseholdId), UserID: target})
		if err != nil {
			return err
		}
		if n == 1 {
			return nil
		}
		// Nothing deleted. Either the target is not a member, or something
		// changed since the check (the caller was removed or demoted, or
		// the target became the owner). Each statement sees the latest
		// committed data, so checking again tells which.
		if err := checkRemove(ctx, q, req.HouseholdId, userID, target); err != nil {
			return err
		}
		if target == userID {
			return errConflict
		}
		return errMemberNotFound
	})
	switch {
	case errors.Is(err, errHouseholdNotFound):
		return openapi.RemoveMember404JSONResponse(householdNotFoundBody()), nil
	case errors.Is(err, errMemberNotFound):
		return openapi.RemoveMember404JSONResponse(errorBody(CodeNotFound, "Member not found.")), nil
	case errors.Is(err, errOwnerOnly):
		return openapi.RemoveMember403JSONResponse{OwnerOnlyJSONResponse: ownerOnlyBody()}, nil
	case errors.Is(err, errOwnerCannotLeave):
		return openapi.RemoveMember409JSONResponse(errorBody(CodeOwnerCannotLeave,
			"The owner cannot leave the household. Transfer ownership to another member or delete the household first.")), nil
	case errors.Is(err, errConflict), isConcurrencyError(err):
		return openapi.RemoveMember409JSONResponse(conflictBody()), nil
	case err != nil:
		return nil, fmt.Errorf("DELETE /households/{id}/members/{userId}: %w", err)
	}
	return openapi.RemoveMember204Response{}, nil
}

// checkRemove returns nil if the caller may remove target from the
// household: the owner removing someone else, or a member leaving. It
// returns errHouseholdNotFound if the caller is not a member,
// errOwnerCannotLeave if the owner is removing themselves and errOwnerOnly if
// a member is removing someone else. Whether target is a member is up to the
// DELETE.
func checkRemove(ctx context.Context, q *db.Queries, householdID types.UUID, userID, target pgtype.UUID) error {
	row, err := getMembership(ctx, q, householdID, userID)
	if err != nil {
		return err
	}
	isOwner := row.Role == db.MemberRoleOwner
	switch {
	case target == userID && isOwner:
		return errOwnerCannotLeave
	case target != userID && !isOwner:
		return errOwnerOnly
	}
	return nil
}

// TransferOwnership makes another member the owner and the caller a member.
// Owner only.
func (a *API) TransferOwnership(ctx context.Context, req openapi.TransferOwnershipRequestObject) (openapi.TransferOwnershipResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	if req.Body == nil {
		return openapi.TransferOwnership400JSONResponse(errorBody(CodeBadRequest, "The request body is required.")), nil
	}
	newOwner := pgUUID(req.Body.UserId)
	var rows []db.ListMembersRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := requireOwner(ctx, q, req.HouseholdId, userID); err != nil {
			return err
		}
		if newOwner == userID {
			return errAlreadyOwner
		}
		// transfer_ownership checks everything again after locking the
		// household, so a transfer that committed since the check above is
		// seen here.
		err := q.TransferOwnership(ctx, db.TransferOwnershipParams{HouseholdID: pgUUID(req.HouseholdId), NewOwnerID: newOwner})
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) {
			switch pgErr.Code {
			case sqlStateInsufficientPrivilege:
				return errOwnerOnly
			case sqlStateInvalidParameterValue:
				return errAlreadyOwner
			case sqlStateNoDataFound:
				return errNotAMember
			}
		}
		if err != nil {
			return err
		}
		rows, err = q.ListMembers(ctx, pgUUID(req.HouseholdId))
		return err
	})
	switch {
	case errors.Is(err, errHouseholdNotFound):
		return openapi.TransferOwnership404JSONResponse{HouseholdNotFoundJSONResponse: householdNotFoundBody()}, nil
	case errors.Is(err, errOwnerOnly):
		return openapi.TransferOwnership403JSONResponse{OwnerOnlyJSONResponse: ownerOnlyBody()}, nil
	case errors.Is(err, errAlreadyOwner):
		return openapi.TransferOwnership400JSONResponse(errorBody(CodeAlreadyOwner, "You are already the owner of this household.")), nil
	case errors.Is(err, errNotAMember):
		return openapi.TransferOwnership400JSONResponse(errorBody(CodeNotAMember, "The new owner must be a member of the household.")), nil
	case isConcurrencyError(err):
		return openapi.TransferOwnership409JSONResponse(conflictBody()), nil
	case err != nil:
		return nil, fmt.Errorf("POST /households/{id}/transfer-ownership: %w", err)
	}
	return openapi.TransferOwnership200JSONResponse(memberListResponse(rows)), nil
}

// isConcurrencyError reports whether err is a database error caused by a
// concurrent change (a second owner, a serialization failure or a deadlock)
// rather than a bug, so the request can be answered with 409 conflict.
func isConcurrencyError(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	switch pgErr.Code {
	case sqlStateUniqueViolation, sqlStateSerializationFailure, sqlStateDeadlockDetected:
		return true
	}
	return false
}

func memberListResponse(rows []db.ListMembersRow) openapi.MemberList {
	list := openapi.MemberList{Members: make([]openapi.Member, 0, len(rows))}
	for _, r := range rows {
		m := openapi.Member{
			UserId:      types.UUID(r.UserID.Bytes),
			DisplayName: r.DisplayName,
			Role:        openapi.MemberRole(r.Role),
			JoinedAt:    r.JoinedAt.Time.UTC().Truncate(time.Microsecond),
		}
		if r.Email.Valid {
			email := r.Email.String
			m.Email = &email
		}
		list.Members = append(list.Members, m)
	}
	return list
}

func conflictBody() openapi.Error {
	return errorBody(CodeConflict, "The household's members changed while this request ran. Try again.")
}

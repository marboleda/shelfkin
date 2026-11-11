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
)

// MaxHouseholdNameLength is the longest household name, in characters, after
// trimming.
const MaxHouseholdNameLength = 50

// Error codes of the households endpoints.
const (
	CodeInvalidName = "invalid_name"
	CodeOwnerOnly   = "owner_only"
)

// errHouseholdNotFound and errOwnerOnly end a household transaction early;
// the handlers turn them into 404 and 403.
var (
	errHouseholdNotFound = errors.New("household not found")
	errOwnerOnly         = errors.New("owner only")
)

// ValidateHouseholdName trims name and reports whether the result is 1 to
// MaxHouseholdNameLength characters long.
func ValidateHouseholdName(name string) (string, bool) {
	name = strings.TrimSpace(name)
	n := utf8.RuneCountInString(name)
	return name, n >= 1 && n <= MaxHouseholdNameLength
}

// ListHouseholds returns the caller's households, sorted by name.
func (a *API) ListHouseholds(ctx context.Context, _ openapi.ListHouseholdsRequestObject) (openapi.ListHouseholdsResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	var rows []db.ListHouseholdsRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		var err error
		rows, err = db.New(tx).ListHouseholds(ctx, userID)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("GET /households: %w", err)
	}
	list := openapi.HouseholdList{Households: make([]openapi.Household, 0, len(rows))}
	for _, r := range rows {
		list.Households = append(list.Households, householdResponse(r.ID, r.Name, r.Role, r.CreatedAt))
	}
	return openapi.ListHouseholds200JSONResponse(list), nil
}

// CreateHousehold creates a household with the caller as its owner.
func (a *API) CreateHousehold(ctx context.Context, req openapi.CreateHouseholdRequestObject) (openapi.CreateHouseholdResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	var name string
	if req.Body != nil {
		name = req.Body.Name
	}
	name, ok = ValidateHouseholdName(name)
	if !ok {
		return openapi.CreateHousehold400JSONResponse{InvalidHouseholdNameJSONResponse: invalidNameBody()}, nil
	}
	var row db.GetHouseholdRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		id, err := q.CreateHousehold(ctx, name)
		if err != nil {
			return err
		}
		row, err = q.GetHousehold(ctx, db.GetHouseholdParams{ID: id, UserID: userID})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("POST /households: %w", err)
	}
	return openapi.CreateHousehold201JSONResponse(householdResponse(row.ID, row.Name, row.Role, row.CreatedAt)), nil
}

// GetHousehold returns one of the caller's households.
func (a *API) GetHousehold(ctx context.Context, req openapi.GetHouseholdRequestObject) (openapi.GetHouseholdResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	var row db.GetHouseholdRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		var err error
		row, err = getMembership(ctx, db.New(tx), req.HouseholdId, userID)
		return err
	})
	if errors.Is(err, errHouseholdNotFound) {
		return openapi.GetHousehold404JSONResponse{HouseholdNotFoundJSONResponse: householdNotFoundBody()}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("GET /households/{id}: %w", err)
	}
	return openapi.GetHousehold200JSONResponse(householdResponse(row.ID, row.Name, row.Role, row.CreatedAt)), nil
}

// UpdateHousehold renames a household. Owner only.
func (a *API) UpdateHousehold(ctx context.Context, req openapi.UpdateHouseholdRequestObject) (openapi.UpdateHouseholdResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	var name string
	if req.Body != nil {
		name = req.Body.Name
	}
	name, ok = ValidateHouseholdName(name)
	if !ok {
		return openapi.UpdateHousehold400JSONResponse{InvalidHouseholdNameJSONResponse: invalidNameBody()}, nil
	}
	var row db.RenameHouseholdRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := requireOwner(ctx, q, req.HouseholdId, userID); err != nil {
			return err
		}
		var err error
		row, err = q.RenameHousehold(ctx, db.RenameHouseholdParams{ID: pgUUID(req.HouseholdId), Name: name})
		if errors.Is(err, pgx.ErrNoRows) {
			// Deleted, or the caller demoted, since the check.
			return errHouseholdNotFound
		}
		return err
	})
	switch {
	case errors.Is(err, errHouseholdNotFound):
		return openapi.UpdateHousehold404JSONResponse{HouseholdNotFoundJSONResponse: householdNotFoundBody()}, nil
	case errors.Is(err, errOwnerOnly):
		return openapi.UpdateHousehold403JSONResponse{OwnerOnlyJSONResponse: ownerOnlyBody()}, nil
	case err != nil:
		return nil, fmt.Errorf("PATCH /households/{id}: %w", err)
	}
	return openapi.UpdateHousehold200JSONResponse(householdResponse(row.ID, row.Name, db.MemberRoleOwner, row.CreatedAt)), nil
}

// DeleteHousehold deletes a household and everything in it. Owner only.
func (a *API) DeleteHousehold(ctx context.Context, req openapi.DeleteHouseholdRequestObject) (openapi.DeleteHouseholdResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if err := requireOwner(ctx, q, req.HouseholdId, userID); err != nil {
			return err
		}
		n, err := q.DeleteHousehold(ctx, pgUUID(req.HouseholdId))
		if err != nil {
			return err
		}
		if n == 0 {
			// Deleted, or the caller demoted, since the check.
			return errHouseholdNotFound
		}
		return nil
	})
	switch {
	case errors.Is(err, errHouseholdNotFound):
		return openapi.DeleteHousehold404JSONResponse{HouseholdNotFoundJSONResponse: householdNotFoundBody()}, nil
	case errors.Is(err, errOwnerOnly):
		return openapi.DeleteHousehold403JSONResponse{OwnerOnlyJSONResponse: ownerOnlyBody()}, nil
	case err != nil:
		return nil, fmt.Errorf("DELETE /households/{id}: %w", err)
	}
	return openapi.DeleteHousehold204Response{}, nil
}

// getMembership returns the household with the caller's role, or
// errHouseholdNotFound if it does not exist or the caller is not a member.
func getMembership(ctx context.Context, q *db.Queries, householdID types.UUID, userID pgtype.UUID) (db.GetHouseholdRow, error) {
	row, err := q.GetHousehold(ctx, db.GetHouseholdParams{ID: pgUUID(householdID), UserID: userID})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, errHouseholdNotFound
	}
	return row, err
}

// requireOwner returns errHouseholdNotFound if the caller is not a member of
// the household and errOwnerOnly if they are a member but not the owner.
func requireOwner(ctx context.Context, q *db.Queries, householdID types.UUID, userID pgtype.UUID) error {
	row, err := getMembership(ctx, q, householdID, userID)
	if err != nil {
		return err
	}
	if row.Role != db.MemberRoleOwner {
		return errOwnerOnly
	}
	return nil
}

func pgUUID(id types.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func householdResponse(id pgtype.UUID, name string, role db.MemberRole, createdAt pgtype.Timestamptz) openapi.Household {
	return openapi.Household{
		Id:        types.UUID(id.Bytes),
		Name:      name,
		Role:      openapi.MemberRole(role),
		CreatedAt: createdAt.Time.UTC().Truncate(time.Microsecond),
	}
}

func invalidNameBody() openapi.InvalidHouseholdNameJSONResponse {
	return openapi.InvalidHouseholdNameJSONResponse(errorBody(CodeInvalidName,
		fmt.Sprintf("name must be 1 to %d characters after trimming spaces.", MaxHouseholdNameLength)))
}

func ownerOnlyBody() openapi.OwnerOnlyJSONResponse {
	return openapi.OwnerOnlyJSONResponse(errorBody(CodeOwnerOnly, "Only the household owner can do this."))
}

func householdNotFoundBody() openapi.HouseholdNotFoundJSONResponse {
	return openapi.HouseholdNotFoundJSONResponse(errorBody(CodeNotFound, "Household not found."))
}

package server

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/oapi-codegen/runtime/types"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/db"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// Limits of GET /households/{id}/catalog/search.
const (
	// MaxSearchQueryLength is the longest q, in characters, after trimming.
	MaxSearchQueryLength = 100
	DefaultSearchLimit   = 10
	MinSearchLimit       = 1
	MaxSearchLimit       = 25
)

// Error codes of the catalog search endpoint.
const (
	CodeInvalidQuery = "invalid_query"
	CodeInvalidLimit = "invalid_limit"
)

// ValidateSearchQuery trims q and reports whether the result is at most
// MaxSearchQueryLength characters long. An empty result is valid.
func ValidateSearchQuery(q string) (string, bool) {
	q = strings.TrimSpace(q)
	return q, utf8.RuneCountInString(q) <= MaxSearchQueryLength
}

// SearchCatalog returns the household's best-matching catalog items for
// type-ahead. It runs one SQL query, under row-level security, that checks
// membership, matches, ranks and limits in the database (see SearchCatalog in
// db/queries/catalog.sql).
func (a *API) SearchCatalog(ctx context.Context, req openapi.SearchCatalogRequestObject) (openapi.SearchCatalogResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	var q string
	if req.Params.Q != nil {
		q = *req.Params.Q
	}
	q, ok = ValidateSearchQuery(q)
	if !ok {
		return openapi.SearchCatalog400JSONResponse(errorBody(CodeInvalidQuery,
			fmt.Sprintf("q must be at most %d characters after trimming spaces.", MaxSearchQueryLength))), nil
	}
	limit := DefaultSearchLimit
	if req.Params.Limit != nil {
		limit = *req.Params.Limit
	}
	if limit < MinSearchLimit || limit > MaxSearchLimit {
		return openapi.SearchCatalog400JSONResponse(errorBody(CodeInvalidLimit,
			fmt.Sprintf("limit must be %d to %d.", MinSearchLimit, MaxSearchLimit))), nil
	}

	var rows []db.SearchCatalogRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		var err error
		// An empty q still runs the query, so a non-member gets 404 rather
		// than an empty list.
		rows, err = db.New(tx).SearchCatalog(ctx, db.SearchCatalogParams{
			HouseholdID: pgUUID(req.HouseholdId),
			Q:           q,
			MaxResults:  int32(limit),
		})
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("GET /households/{id}/catalog/search: %w", err)
	}
	// No row: the household does not exist or the caller is not a member.
	if len(rows) == 0 {
		return openapi.SearchCatalog404JSONResponse{HouseholdNotFoundJSONResponse: householdNotFoundBody()}, nil
	}
	list := openapi.CatalogItemList{Items: make([]openapi.CatalogItem, 0, len(rows))}
	for _, r := range rows {
		// A member with no matches gets one row with no item. The item's
		// columns are nullable only because of that LEFT JOIN.
		if !r.CatalogItemID.Valid {
			continue
		}
		item := openapi.CatalogItem{
			Id:         types.UUID(r.CatalogItemID.Bytes),
			Name:       r.Name.String,
			IsCustom:   r.IsCustom.Bool,
			CategoryId: int(r.CategoryID.Int16),
			LocationId: int(r.LocationID.Int16),
		}
		if r.ShelfLifeDays.Valid {
			days := int(r.ShelfLifeDays.Int32)
			item.ShelfLifeDays = &days
		}
		list.Items = append(list.Items, item)
	}
	return openapi.SearchCatalog200JSONResponse(list), nil
}

// ListCategories returns every category in display order.
func (a *API) ListCategories(ctx context.Context, _ openapi.ListCategoriesRequestObject) (openapi.ListCategoriesResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	var rows []db.ListCategoriesRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		var err error
		rows, err = db.New(tx).ListCategories(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("GET /categories: %w", err)
	}
	list := openapi.CategoryList{Items: make([]openapi.Category, 0, len(rows))}
	for _, r := range rows {
		list.Items = append(list.Items, openapi.Category{Id: int(r.ID), Key: r.Key, Name: r.Name})
	}
	return openapi.ListCategories200JSONResponse(list), nil
}

// ListLocations returns every storage location in display order.
func (a *API) ListLocations(ctx context.Context, _ openapi.ListLocationsRequestObject) (openapi.ListLocationsResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	var rows []db.ListLocationsRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		var err error
		rows, err = db.New(tx).ListLocations(ctx)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("GET /locations: %w", err)
	}
	list := openapi.LocationList{Items: make([]openapi.Location, 0, len(rows))}
	for _, r := range rows {
		list.Items = append(list.Items, openapi.Location{Id: int(r.ID), Key: r.Key, Name: r.Name})
	}
	return openapi.ListLocations200JSONResponse(list), nil
}

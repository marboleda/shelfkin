package server

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oapi-codegen/nullable"
	"github.com/oapi-codegen/runtime/types"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/db"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// Limits of the custom item endpoints.
const (
	// MaxCatalogItemNameLength is the longest custom item name, in
	// characters, after cleaning (see CleanCatalogItemName).
	MaxCatalogItemNameLength = 60
	MinShelfLifeDays         = 1
	MaxShelfLifeDays         = 3650
)

// Error codes of the custom item endpoints. invalid_name and
// invalid_location are shared with other endpoints.
const (
	CodeInvalidCategory  = "invalid_category"
	CodeInvalidShelfLife = "invalid_shelf_life"
	CodeNameTaken        = "name_taken"
	CodeNotOverridable   = "not_overridable"
)

// These end a catalog item transaction early; the handlers turn them into
// 400, 404 and 409 responses.
var (
	errCatalogItemNotFound = errors.New("catalog item not found")
	errInvalidCategory     = errors.New("invalid category")
	errNameTaken           = errors.New("name taken")
	errNotOverridable      = errors.New("shelf life not overridable")
	errNullCategory        = errors.New("null category for a custom item")
	errNullLocation        = errors.New("null location for a custom item")
	errInvalidItemName     = errors.New("invalid item name")
	errInvalidShelfLife    = errors.New("invalid shelf life")
)

// CleanCatalogItemName trims name and collapses every inner run of
// whitespace to one space. It reports whether the result is 1 to
// MaxCatalogItemNameLength characters long.
func CleanCatalogItemName(name string) (string, bool) {
	name = strings.Join(strings.Fields(name), " ")
	n := utf8.RuneCountInString(name)
	return name, n >= 1 && n <= MaxCatalogItemNameLength
}

// ValidShelfLifeDays reports whether d is an allowed shelf life.
func ValidShelfLifeDays(d int) bool {
	return d >= MinShelfLifeDays && d <= MaxShelfLifeDays
}

// checkCategory returns errInvalidCategory unless id is a category.
func checkCategory(ctx context.Context, q *db.Queries, id int) (int16, error) {
	if id < 1 || id > math.MaxInt16 {
		return 0, errInvalidCategory
	}
	ok, err := q.CategoryExists(ctx, int16(id))
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, errInvalidCategory
	}
	return int16(id), nil
}

// getCatalogItem returns the item as the household sees it, or
// errCatalogItemNotFound if the household does not exist, the caller is not a
// member, or the item is not a built-in or this household's custom item.
func getCatalogItem(ctx context.Context, q *db.Queries, householdID, itemID pgtype.UUID) (db.GetCatalogItemRow, error) {
	row, err := q.GetCatalogItem(ctx, db.GetCatalogItemParams{HouseholdID: householdID, CatalogItemID: itemID})
	if errors.Is(err, pgx.ErrNoRows) {
		return row, errCatalogItemNotFound
	}
	return row, err
}

func catalogItemResponse(r db.GetCatalogItemRow) openapi.CatalogItem {
	item := openapi.CatalogItem{
		Id:                 types.UUID(r.CatalogItemID.Bytes),
		Name:               r.Name,
		IsCustom:           r.IsCustom,
		CategoryId:         int(r.CategoryID),
		LocationId:         int(r.LocationID),
		CategoryOverridden: r.CategoryOverridden,
		LocationOverridden: r.LocationOverridden,
	}
	if r.ShelfLifeDays.Valid {
		days := int(r.ShelfLifeDays.Int32)
		item.ShelfLifeDays = &days
	}
	return item
}

func pgErrorCode(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

// CreateCatalogItem adds a custom item to the household's catalog.
func (a *API) CreateCatalogItem(ctx context.Context, req openapi.CreateCatalogItemRequestObject) (openapi.CreateCatalogItemResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	body := req.Body
	if body == nil {
		body = &openapi.CreateCatalogItem{}
	}
	householdID := pgUUID(req.HouseholdId)

	var result db.GetCatalogItemRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		// Membership first, so a non-member gets 404 whatever the body.
		if _, err := getMembership(ctx, q, req.HouseholdId, userID); err != nil {
			return err
		}
		name, ok := CleanCatalogItemName(body.Name)
		if !ok {
			return errInvalidItemName
		}
		var shelfLife pgtype.Int4
		if body.ShelfLifeDays != nil {
			if !ValidShelfLifeDays(*body.ShelfLifeDays) {
				return errInvalidShelfLife
			}
			shelfLife = pgtype.Int4{Int32: int32(*body.ShelfLifeDays), Valid: true}
		}
		categoryID, err := checkCategory(ctx, q, body.CategoryId)
		if err != nil {
			return err
		}
		locationID, err := checkLocation(ctx, q, body.LocationId)
		if err != nil {
			return err
		}
		taken, err := q.CatalogNameTaken(ctx, db.CatalogNameTakenParams{HouseholdID: householdID, Name: name})
		if err != nil {
			return err
		}
		if taken {
			return errNameTaken
		}
		// A concurrent create of the same name in this household fails here
		// with a unique violation (23505), answered with 409 below.
		id, err := q.InsertCustomItem(ctx, db.InsertCustomItemParams{
			HouseholdID:   householdID,
			Name:          name,
			CategoryID:    categoryID,
			LocationID:    locationID,
			ShelfLifeDays: shelfLife,
			CreatedBy:     userID,
		})
		if err != nil {
			return err
		}
		result, err = getCatalogItem(ctx, q, householdID, id)
		return err
	})
	switch {
	case errors.Is(err, errHouseholdNotFound):
		return openapi.CreateCatalogItem404JSONResponse{HouseholdNotFoundJSONResponse: householdNotFoundBody()}, nil
	case errors.Is(err, errInvalidItemName):
		return openapi.CreateCatalogItem400JSONResponse(errorBody(CodeInvalidName,
			fmt.Sprintf("name must be 1 to %d characters after trimming spaces.", MaxCatalogItemNameLength))), nil
	case errors.Is(err, errInvalidShelfLife):
		return openapi.CreateCatalogItem400JSONResponse(invalidShelfLifeBody()), nil
	case errors.Is(err, errInvalidCategory):
		return openapi.CreateCatalogItem400JSONResponse(invalidCategoryBody()), nil
	case errors.Is(err, errInvalidLocation):
		return openapi.CreateCatalogItem400JSONResponse(invalidLocationBody()), nil
	case errors.Is(err, errNameTaken), pgErrorCode(err) == sqlStateUniqueViolation:
		return openapi.CreateCatalogItem409JSONResponse(nameTakenBody()), nil
	case err != nil:
		return nil, fmt.Errorf("POST /households/{id}/catalog-items: %w", err)
	}
	return openapi.CreateCatalogItem201JSONResponse(catalogItemResponse(result)), nil
}

// GetCatalogItem returns one built-in or custom item with the household's
// effective defaults.
func (a *API) GetCatalogItem(ctx context.Context, req openapi.GetCatalogItemRequestObject) (openapi.GetCatalogItemResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	var result db.GetCatalogItemRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		var err error
		result, err = getCatalogItem(ctx, db.New(tx), pgUUID(req.HouseholdId), pgUUID(req.ItemId))
		return err
	})
	switch {
	case errors.Is(err, errCatalogItemNotFound):
		return openapi.GetCatalogItem404JSONResponse{CatalogItemNotFoundJSONResponse: catalogItemNotFoundBody()}, nil
	case err != nil:
		return nil, fmt.Errorf("GET /households/{id}/catalog-items/{itemId}: %w", err)
	}
	return openapi.GetCatalogItem200JSONResponse(catalogItemResponse(result)), nil
}

// UpdateCatalogItem changes the household's defaults for an item: the custom
// item's own row, or the household's override of a built-in item.
func (a *API) UpdateCatalogItem(ctx context.Context, req openapi.UpdateCatalogItemRequestObject) (openapi.UpdateCatalogItemResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	body := req.Body
	if body == nil {
		body = &openapi.UpdateCatalogItem{}
	}
	householdID := pgUUID(req.HouseholdId)
	itemID := pgUUID(req.ItemId)

	var result db.GetCatalogItemRow
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		// The item first: a non-member, a missing item and another
		// household's custom item all get 404 before anything is validated
		// or written.
		item, err := getCatalogItem(ctx, q, householdID, itemID)
		if err != nil {
			return err
		}
		category, err := nullableID(ctx, q, body.CategoryId, checkCategory)
		if err != nil {
			return err
		}
		location, err := nullableID(ctx, q, body.LocationId, checkLocation)
		if err != nil {
			return err
		}
		var shelfLife pgtype.Int4
		if body.ShelfLifeDays.IsSpecified() && !body.ShelfLifeDays.IsNull() {
			d := body.ShelfLifeDays.MustGet()
			if !ValidShelfLifeDays(d) {
				return errInvalidShelfLife
			}
			shelfLife = pgtype.Int4{Int32: int32(d), Valid: true}
		}
		setCategory := body.CategoryId.IsSpecified()
		setLocation := body.LocationId.IsSpecified()
		setShelfLife := body.ShelfLifeDays.IsSpecified()
		if !setCategory && !setLocation && !setShelfLife {
			// {}: nothing changes.
			result = item
			return nil
		}

		if item.IsCustom {
			if setCategory && !category.Valid {
				return errNullCategory
			}
			if setLocation && !location.Valid {
				return errNullLocation
			}
			if _, err := q.UpdateCustomItem(ctx, db.UpdateCustomItemParams{
				ID:            itemID,
				HouseholdID:   householdID,
				CategoryID:    category,
				LocationID:    location,
				SetShelfLife:  setShelfLife,
				ShelfLifeDays: shelfLife,
			}); err != nil {
				return err
			}
		} else {
			if setShelfLife {
				return errNotOverridable
			}
			// The built-in default itself is stored as NULL (no override).
			if category.Valid && category.Int16 == item.DefaultCategoryID {
				category = pgtype.Int2{}
			}
			if location.Valid && location.Int16 == item.DefaultLocationID {
				location = pgtype.Int2{}
			}
			err := q.UpsertItemDefaults(ctx, db.UpsertItemDefaultsParams{
				HouseholdID:   householdID,
				CatalogItemID: itemID,
				CategoryID:    category,
				LocationID:    location,
				UpdatedBy:     userID,
				SetCategory:   setCategory,
				SetLocation:   setLocation,
			})
			if pgErrorCode(err) == sqlStateInsufficientPrivilege {
				// The item was checked above, so row-level security (#73)
				// should never reject this. If it does, the check has a bug.
				a.logger.ErrorContext(ctx, "household_item_defaults write rejected by row-level security after the API check",
					"household_id", req.HouseholdId, "catalog_item_id", req.ItemId, "error", err)
				return fmt.Errorf("upsert household_item_defaults: %w", err)
			}
			if err != nil {
				return err
			}
			if err := q.DeleteEmptyItemDefaults(ctx, db.DeleteEmptyItemDefaultsParams{
				HouseholdID: householdID, CatalogItemID: itemID,
			}); err != nil {
				return err
			}
		}
		result, err = getCatalogItem(ctx, q, householdID, itemID)
		return err
	})
	switch {
	case errors.Is(err, errCatalogItemNotFound):
		return openapi.UpdateCatalogItem404JSONResponse{CatalogItemNotFoundJSONResponse: catalogItemNotFoundBody()}, nil
	case errors.Is(err, errInvalidCategory):
		return openapi.UpdateCatalogItem400JSONResponse(invalidCategoryBody()), nil
	case errors.Is(err, errInvalidLocation):
		return openapi.UpdateCatalogItem400JSONResponse(invalidLocationBody()), nil
	case errors.Is(err, errNullCategory):
		return openapi.UpdateCatalogItem400JSONResponse(errorBody(CodeInvalidCategory,
			"categoryId cannot be null for a custom item.")), nil
	case errors.Is(err, errNullLocation):
		return openapi.UpdateCatalogItem400JSONResponse(errorBody(CodeInvalidLocation,
			"locationId cannot be null for a custom item.")), nil
	case errors.Is(err, errInvalidShelfLife):
		return openapi.UpdateCatalogItem400JSONResponse(invalidShelfLifeBody()), nil
	case errors.Is(err, errNotOverridable):
		return openapi.UpdateCatalogItem400JSONResponse(errorBody(CodeNotOverridable,
			"The shelf life of a built-in item cannot be changed.")), nil
	case err != nil:
		return nil, fmt.Errorf("PATCH /households/{id}/catalog-items/{itemId}: %w", err)
	}
	return openapi.UpdateCatalogItem200JSONResponse(catalogItemResponse(result)), nil
}

// nullableID checks a nullable category or location ID with check. It returns
// a NULL Int2 when v is omitted or null.
func nullableID(ctx context.Context, q *db.Queries, v nullable.Nullable[int],
	check func(context.Context, *db.Queries, int) (int16, error)) (pgtype.Int2, error) {
	if !v.IsSpecified() || v.IsNull() {
		return pgtype.Int2{}, nil
	}
	id, err := check(ctx, q, v.MustGet())
	if err != nil {
		return pgtype.Int2{}, err
	}
	return pgtype.Int2{Int16: id, Valid: true}, nil
}

func invalidCategoryBody() openapi.Error {
	return errorBody(CodeInvalidCategory, "categoryId must be the ID of a category.")
}

func invalidShelfLifeBody() openapi.Error {
	return errorBody(CodeInvalidShelfLife,
		fmt.Sprintf("shelfLifeDays must be a whole number from %d to %d, or null.", MinShelfLifeDays, MaxShelfLifeDays))
}

func nameTakenBody() openapi.Error {
	return errorBody(CodeNameTaken, "An item with this name already exists.")
}

func catalogItemNotFoundBody() openapi.CatalogItemNotFoundJSONResponse {
	return openapi.CatalogItemNotFoundJSONResponse(errorBody(CodeNotFound, "Catalog item not found."))
}

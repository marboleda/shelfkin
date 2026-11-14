package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oapi-codegen/runtime/types"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/db"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
	"github.com/marboleda/shelfkin/api/internal/events"
	"github.com/marboleda/shelfkin/api/internal/expiry"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// CodeInvalidFilter is the error code of a locationId or categoryId filter
// value of GET /households/{id}/pantry that is not a location or category ID.
const CodeInvalidFilter = "invalid_filter"

// ExpirySoonDays is the "expiring soon" window of the pantry's expiryStatus:
// an entry expiring from today to today + ExpirySoonDays days is soon. It is
// the expiry job's window (docs/data-model.md, Expiry rules), so the pantry
// and the push agree.
const ExpirySoonDays = expiry.SoonDays

// These end the pantry transaction early; GetPantry turns them into 400s.
var (
	errInvalidLocationFilter = errors.New("invalid location filter")
	errInvalidCategoryFilter = errors.New("invalid category filter")
)

// ExpiryStatus returns an entry's expiry status on the date today: expired
// when expiresOn is before today, soon when it is from today to today +
// ExpirySoonDays days, and nil otherwise, when there is no expiry date, or
// when the entry is out.
func ExpiryStatus(level db.StockLevel, expiresOn, today pgtype.Date) *openapi.ExpiryStatus {
	if level == db.StockLevelOut || !expiresOn.Valid || !today.Valid {
		return nil
	}
	var s openapi.ExpiryStatus
	switch {
	case expiresOn.Time.Before(today.Time):
		s = openapi.ExpiryStatusExpired
	case !expiresOn.Time.After(today.Time.AddDate(0, 0, ExpirySoonDays)):
		s = openapi.ExpiryStatusSoon
	default:
		return nil
	}
	return &s
}

// ParseFilterIDs parses the values of a repeated ID filter. It returns the
// distinct IDs, in the order given, and false if a value is not a positive
// integer that fits a smallint (so cannot be a location or category ID).
func ParseFilterIDs(values *[]string) ([]int16, bool) {
	if values == nil {
		return []int16{}, true
	}
	ids := make([]int16, 0, len(*values))
	seen := make(map[int16]bool, len(*values))
	for _, v := range *values {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > math.MaxInt16 {
			return nil, false
		}
		id := int16(n)
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids, true
}

// GetPantry returns the household's pantry grouped by catalog item. It runs a
// fixed number of queries whatever the number of entries: the membership
// check, the caller's time zone, the filter check (only with filters) and
// ListPantry, which loads every entry with its item and latest event.
func (a *API) GetPantry(ctx context.Context, req openapi.GetPantryRequestObject) (openapi.GetPantryResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	locationIDs, okLoc := ParseFilterIDs(req.Params.LocationId)
	categoryIDs, okCat := ParseFilterIDs(req.Params.CategoryId)
	if !okLoc {
		return openapi.GetPantry400JSONResponse(invalidLocationFilterBody()), nil
	}
	if !okCat {
		return openapi.GetPantry400JSONResponse(invalidCategoryFilterBody()), nil
	}

	var (
		today pgtype.Date
		rows  []db.ListPantryRow
	)
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if _, err := getMembership(ctx, q, req.HouseholdId, userID); err != nil {
			return err
		}
		if len(locationIDs) > 0 || len(categoryIDs) > 0 {
			n, err := q.CountPantryFilters(ctx, db.CountPantryFiltersParams{LocationIds: locationIDs, CategoryIds: categoryIDs})
			if err != nil {
				return err
			}
			if int(n.Locations) != len(locationIDs) {
				return errInvalidLocationFilter
			}
			if int(n.Categories) != len(categoryIDs) {
				return errInvalidCategoryFilter
			}
		}
		user, err := q.GetUser(ctx, userID)
		if err != nil {
			return err
		}
		today = a.today(user.Timezone)
		rows, err = q.ListPantry(ctx, db.ListPantryParams{
			HouseholdID: pgUUID(req.HouseholdId),
			LocationIds: locationIDs,
			CategoryIds: categoryIDs,
		})
		return err
	})
	switch {
	case errors.Is(err, errHouseholdNotFound):
		return openapi.GetPantry404JSONResponse{HouseholdNotFoundJSONResponse: householdNotFoundBody()}, nil
	case errors.Is(err, errInvalidLocationFilter):
		return openapi.GetPantry400JSONResponse(invalidLocationFilterBody()), nil
	case errors.Is(err, errInvalidCategoryFilter):
		return openapi.GetPantry400JSONResponse(invalidCategoryFilterBody()), nil
	case err != nil:
		return nil, fmt.Errorf("GET /households/{id}/pantry: %w", err)
	}

	groups, err := pantryGroups(rows, today)
	if err != nil {
		return nil, fmt.Errorf("GET /households/{id}/pantry: %w", err)
	}
	return openapi.GetPantry200JSONResponse(openapi.Pantry{
		Today:  types.Date{Time: today.Time},
		Groups: groups,
	}), nil
}

// pantryGroups groups ListPantry's rows, in which every item's entries are
// consecutive, into one group per item, keeping the order.
func pantryGroups(rows []db.ListPantryRow, today pgtype.Date) ([]openapi.PantryGroup, error) {
	groups := []openapi.PantryGroup{}
	for _, r := range rows {
		itemID := types.UUID(r.CatalogItemID.Bytes)
		if len(groups) == 0 || groups[len(groups)-1].CatalogItemId != itemID {
			groups = append(groups, openapi.PantryGroup{
				CatalogItemId: itemID,
				Name:          r.Name,
				IsCustom:      r.IsCustom,
				CategoryId:    int(r.CategoryID),
				Entries:       []openapi.PantryListEntry{},
			})
		}
		e, err := pantryListEntry(r, today)
		if err != nil {
			return nil, err
		}
		g := &groups[len(groups)-1]
		g.Entries = append(g.Entries, e)
	}
	return groups, nil
}

func pantryListEntry(r db.ListPantryRow, today pgtype.Date) (openapi.PantryListEntry, error) {
	e := openapi.PantryListEntry{
		Id:            types.UUID(r.ID.Bytes),
		CatalogItemId: types.UUID(r.CatalogItemID.Bytes),
		LocationId:    int(r.LocationID),
		Level:         openapi.StockLevel(r.Level),
		PurchasedOn:   types.Date{Time: r.PurchasedOn.Time},
		CreatedAt:     r.CreatedAt.Time.UTC().Truncate(time.Microsecond),
		UpdatedAt:     r.UpdatedAt.Time.UTC().Truncate(time.Microsecond),
		ExpiryStatus:  ExpiryStatus(r.Level, r.ExpiresOn, today),
	}
	if r.ExpiresOn.Valid {
		e.ExpiresOn = &types.Date{Time: r.ExpiresOn.Time}
	}
	// No event: only possible for rows written outside the API.
	if !r.EventType.Valid {
		return e, nil
	}
	var changes events.Changes
	if err := json.Unmarshal(r.EventChanges, &changes); err != nil {
		return e, fmt.Errorf("item event changes of entry %s: %w", e.Id, err)
	}
	lc := openapi.LastChange{
		Type:       openapi.ItemEventType(r.EventType.EventType),
		Changes:    make(map[string]openapi.EntryChange, len(changes)),
		OccurredAt: r.EventOccurredAt.Time.UTC().Truncate(time.Microsecond),
	}
	for col, c := range changes {
		lc.Changes[col] = openapi.EntryChange{From: c.From, To: c.To}
	}
	if r.EventActorID.Valid {
		id := types.UUID(r.EventActorID.Bytes)
		lc.ActorId = &id
	}
	if r.EventActorName.Valid {
		name := r.EventActorName.String
		lc.ActorName = &name
	}
	e.LastChange = &lc
	return e, nil
}

func invalidLocationFilterBody() openapi.Error {
	return errorBody(CodeInvalidFilter, "Every locationId must be the ID of a location.")
}

func invalidCategoryFilterBody() openapi.Error {
	return errorBody(CodeInvalidFilter, "Every categoryId must be the ID of a category.")
}

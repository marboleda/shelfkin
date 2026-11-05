package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/oapi-codegen/runtime/types"

	"github.com/marboleda/shelfkin/api/internal/auth"
	"github.com/marboleda/shelfkin/api/internal/db"
	"github.com/marboleda/shelfkin/api/internal/dbtx"
	"github.com/marboleda/shelfkin/api/internal/events"
	"github.com/marboleda/shelfkin/api/internal/openapi"
)

// Error codes of the pantry entry endpoints.
const (
	CodeInvalidID          = "invalid_id"
	CodeInvalidCatalogItem = "invalid_catalog_item"
	CodeInvalidLocation    = "invalid_location"
	CodeInvalidLevel       = "invalid_level"
	CodeOneFieldPerUpdate  = "one_field_per_update"
	CodeEventIDConflict    = "event_id_conflict"
	CodeEntryIDConflict    = "entry_id_conflict"
)

// These end an entry transaction early (rolling it back); the handlers turn
// them into 400, 404 and 409 responses.
var (
	errEntryNotFound       = errors.New("entry not found")
	errInvalidCatalogItem  = errors.New("invalid catalog item")
	errInvalidLocation     = errors.New("invalid location")
	errEventIDConflict     = errors.New("event id conflict")
	errEntryIDConflict     = errors.New("entry id conflict")
	errEntryAlreadyDeleted = errors.New("entry already deleted by this event")
)

// ParseUUIDv7 parses s as a UUID in its canonical 36-character form and
// reports whether it is a version 7 UUID.
func ParseUUIDv7(s string) (pgtype.UUID, bool) {
	if len(s) != 36 {
		return pgtype.UUID{}, false
	}
	u, err := uuid.Parse(s)
	if err != nil || u.Version() != 7 {
		return pgtype.UUID{}, false
	}
	return pgtype.UUID{Bytes: u, Valid: true}, true
}

// entry is a pantry_entries row with its catalog item's name (empty when the
// query did not load it).
type entry struct {
	ID            pgtype.UUID
	HouseholdID   pgtype.UUID
	CatalogItemID pgtype.UUID
	LocationID    int16
	Level         db.StockLevel
	PurchasedOn   pgtype.Date
	ExpiresOn     pgtype.Date
	CreatedAt     pgtype.Timestamptz
	UpdatedAt     pgtype.Timestamptz
	Name          string
}

// writtenEntry is a row returned by InsertEntry or UpdateEntry, with the
// catalog item's name.
func writtenEntry(r db.UpdateEntryRow, name string) entry {
	return entry{
		ID: r.ID, HouseholdID: r.HouseholdID, CatalogItemID: r.CatalogItemID,
		LocationID: r.LocationID, Level: r.Level, PurchasedOn: r.PurchasedOn,
		ExpiresOn: r.ExpiresOn, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		Name: name,
	}
}

func (e entry) response() openapi.PantryEntry {
	r := openapi.PantryEntry{
		Id:            types.UUID(e.ID.Bytes),
		CatalogItemId: types.UUID(e.CatalogItemID.Bytes),
		LocationId:    int(e.LocationID),
		Level:         openapi.StockLevel(e.Level),
		PurchasedOn:   types.Date{Time: e.PurchasedOn.Time},
		CreatedAt:     e.CreatedAt.Time.UTC().Truncate(time.Microsecond),
		UpdatedAt:     e.UpdatedAt.Time.UTC().Truncate(time.Microsecond),
	}
	if e.ExpiresOn.Valid {
		r.ExpiresOn = &types.Date{Time: e.ExpiresOn.Time}
	}
	return r
}

// snapshot is the entry as events.Snapshot. e.Name must be set.
func (e entry) snapshot() events.Snapshot {
	return events.Snapshot{
		Name:          e.Name,
		CatalogItemID: uuid.UUID(e.CatalogItemID.Bytes).String(),
		Level:         string(e.Level),
		LocationID:    int(e.LocationID),
		PurchasedOn:   dateString(e.PurchasedOn),
		ExpiresOn:     datePtr(e.ExpiresOn),
	}
}

func dateString(d pgtype.Date) string { return d.Time.Format(events.DateLayout) }

func datePtr(d pgtype.Date) *string {
	if !d.Valid {
		return nil
	}
	s := dateString(d)
	return &s
}

// dateValue is d for a Change: a date string, or nil for no date.
func dateValue(d pgtype.Date) any {
	if !d.Valid {
		return nil
	}
	return dateString(d)
}

func pgDate(t time.Time) pgtype.Date {
	return pgtype.Date{Time: time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC), Valid: true}
}

func sameDate(a, b pgtype.Date) bool {
	if a.Valid != b.Valid {
		return false
	}
	return !a.Valid || dateString(a) == dateString(b)
}

func validLevel(l openapi.StockLevel) bool {
	switch l {
	case openapi.StockLevelFull, openapi.StockLevelHalf, openapi.StockLevelLow, openapi.StockLevelOut:
		return true
	}
	return false
}

// checkLocation returns errInvalidLocation unless id is a location.
func checkLocation(ctx context.Context, q *db.Queries, id int) (int16, error) {
	if id < 1 || id > math.MaxInt16 {
		return 0, errInvalidLocation
	}
	ok, err := q.LocationExists(ctx, int16(id))
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, errInvalidLocation
	}
	return int16(id), nil
}

// lookupEvent returns the event with this ID if the caller can see it. An
// event of a household the caller is not in looks the same as no event.
func lookupEvent(ctx context.Context, q *db.Queries, id pgtype.UUID) (db.GetEventRow, bool, error) {
	ev, err := q.GetEvent(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ev, false, nil
	}
	return ev, err == nil, err
}

// eventParams describes the event that goes with an entry change.
type eventParams struct {
	ID      pgtype.UUID
	Actor   pgtype.UUID
	Type    db.EventType
	Changes events.Changes
	// Entry is the entry after the change (before it, for entry_deleted).
	Entry entry
}

// insertEvent writes the event. It returns errEventIDConflict, which rolls
// back the entry change, when an event with this ID already exists. The
// caller has already checked for a visible event with this ID, so that one
// belongs to another household or to a concurrent request.
func insertEvent(ctx context.Context, q *db.Queries, p eventParams) error {
	changes, err := p.Changes.Marshal()
	if err != nil {
		return err
	}
	snapshot, err := json.Marshal(p.Entry.snapshot())
	if err != nil {
		return err
	}
	n, err := q.InsertEvent(ctx, db.InsertEventParams{
		ID:            p.ID,
		HouseholdID:   p.Entry.HouseholdID,
		EntryID:       p.Entry.ID,
		CatalogItemID: p.Entry.CatalogItemID,
		ActorID:       p.Actor,
		Type:          p.Type,
		Changes:       changes,
		Snapshot:      snapshot,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return errEventIDConflict
	}
	return nil
}

// today is the current date in the IANA time zone tz (UTC if tz is not one).
func (a *API) today(tz string) pgtype.Date {
	loc, err := time.LoadLocation(tz)
	if err != nil || tz == "" || tz == "Local" {
		loc = time.UTC
	}
	return pgDate(a.now().In(loc))
}

// CreateEntry adds a pantry entry and its entry_created event.
func (a *API) CreateEntry(ctx context.Context, req openapi.CreateEntryRequestObject) (openapi.CreateEntryResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	body := req.Body
	if body == nil {
		body = &openapi.CreateEntry{}
	}
	entryID, ok1 := ParseUUIDv7(body.Id)
	eventID, ok2 := ParseUUIDv7(body.EventId)
	if !ok1 || !ok2 {
		return openapi.CreateEntry400JSONResponse(errorBody(CodeInvalidID, "id and eventId must be UUIDv7s.")), nil
	}
	level := db.StockLevelFull
	if body.Level != nil {
		if !validLevel(*body.Level) {
			return openapi.CreateEntry400JSONResponse(invalidLevelBody()), nil
		}
		level = db.StockLevel(*body.Level)
	}
	householdID := pgUUID(req.HouseholdId)
	catalogItemID := pgUUID(body.CatalogItemId)

	var result entry
	created := false
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if _, err := getMembership(ctx, q, req.HouseholdId, userID); err != nil {
			return err
		}
		// Checked before anything is written. Row-level security (#73)
		// rejects another household's item too, but with 42501 (a 500 here).
		item, err := q.GetEntryCatalogItem(ctx, db.GetEntryCatalogItemParams{HouseholdID: householdID, CatalogItemID: catalogItemID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvalidCatalogItem
		}
		if err != nil {
			return err
		}
		locationID := item.LocationID
		if body.LocationId != nil {
			if locationID, err = checkLocation(ctx, q, *body.LocationId); err != nil {
				return err
			}
		}

		// A visible event with this ID: a retry of this create, or a reused ID.
		ev, found, err := lookupEvent(ctx, q, eventID)
		if err != nil {
			return err
		}
		if found {
			if ev.HouseholdID != householdID || ev.EntryID != entryID ||
				ev.Type != db.EventTypeEntryCreated || ev.CatalogItemID != catalogItemID {
				return errEventIDConflict
			}
			result, err = existingEntry(ctx, q, entryID, householdID, catalogItemID)
			return err
		}

		purchasedOn := pgtype.Date{}
		if body.PurchasedOn != nil {
			purchasedOn = pgDate(body.PurchasedOn.Time)
		} else {
			user, err := q.GetUser(ctx, userID)
			if err != nil {
				return err
			}
			purchasedOn = a.today(user.Timezone)
		}
		var expiresOn pgtype.Date
		switch {
		case body.ExpiresOn.IsNull():
			// Explicitly no expiry.
		case body.ExpiresOn.IsSpecified():
			expiresOn = pgDate(body.ExpiresOn.MustGet().Time)
		case item.ShelfLifeDays.Valid:
			expiresOn = pgDate(purchasedOn.Time.AddDate(0, 0, int(item.ShelfLifeDays.Int32)))
		}

		row, err := q.InsertEntry(ctx, db.InsertEntryParams{
			ID:            entryID,
			HouseholdID:   householdID,
			CatalogItemID: catalogItemID,
			LocationID:    locationID,
			Level:         level,
			PurchasedOn:   purchasedOn,
			ExpiresOn:     expiresOn,
			ActorID:       userID,
		})
		if errors.Is(err, pgx.ErrNoRows) {
			// The entry ID is taken: by this entry (a retry that raced this
			// one, or the same entry under another eventId), or by an entry of
			// another item or household.
			result, err = existingEntry(ctx, q, entryID, householdID, catalogItemID)
			return err
		}
		if err != nil {
			return err
		}
		result = writtenEntry(db.UpdateEntryRow(row), item.Name)
		created = true
		return insertEvent(ctx, q, eventParams{ID: eventID, Actor: userID, Type: db.EventTypeEntryCreated, Entry: result})
	})
	switch {
	case errors.Is(err, errHouseholdNotFound):
		return openapi.CreateEntry404JSONResponse{HouseholdNotFoundJSONResponse: householdNotFoundBody()}, nil
	case errors.Is(err, errInvalidCatalogItem):
		return openapi.CreateEntry400JSONResponse(errorBody(CodeInvalidCatalogItem,
			"catalogItemId must be a built-in item or one of this household's items.")), nil
	case errors.Is(err, errInvalidLocation):
		return openapi.CreateEntry400JSONResponse(invalidLocationBody()), nil
	case errors.Is(err, errEventIDConflict):
		return openapi.CreateEntry409JSONResponse{EntryConflictJSONResponse: eventIDConflictBody()}, nil
	case errors.Is(err, errEntryIDConflict):
		return openapi.CreateEntry409JSONResponse{EntryConflictJSONResponse: entryIDConflictBody()}, nil
	case err != nil:
		return nil, fmt.Errorf("POST /households/{id}/entries: %w", err)
	}
	if created {
		return openapi.CreateEntry201JSONResponse(result.response()), nil
	}
	return openapi.CreateEntry200JSONResponse(result.response()), nil
}

// existingEntry returns the entry with this ID for a create that found the ID
// taken. It is a retry only if the caller can see the entry and it is in the
// same household for the same catalog item; anything else, including an
// entry of a household the caller cannot see, is errEntryIDConflict.
func existingEntry(ctx context.Context, q *db.Queries, id, householdID, catalogItemID pgtype.UUID) (entry, error) {
	row, err := q.GetEntryByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return entry{}, errEntryIDConflict
	}
	if err != nil {
		return entry{}, err
	}
	if row.HouseholdID != householdID || row.CatalogItemID != catalogItemID {
		return entry{}, errEntryIDConflict
	}
	return entry(row), nil
}

// UpdateEntry changes one field of an entry and records its event.
func (a *API) UpdateEntry(ctx context.Context, req openapi.UpdateEntryRequestObject) (openapi.UpdateEntryResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	body := req.Body
	if body == nil {
		body = &openapi.UpdateEntry{}
	}
	eventID, ok := ParseUUIDv7(body.EventId)
	if !ok {
		return openapi.UpdateEntry400JSONResponse(errorBody(CodeInvalidID, "eventId must be a UUIDv7.")), nil
	}
	fields := 0
	var eventType db.EventType
	if body.Level != nil {
		fields++
		eventType = db.EventTypeLevelChanged
	}
	if body.LocationId != nil {
		fields++
		eventType = db.EventTypeLocationChanged
	}
	if body.PurchasedOn != nil {
		fields++
		eventType = db.EventTypePurchaseDateChanged
	}
	if body.ExpiresOn.IsSpecified() {
		fields++
		eventType = db.EventTypeExpiryChanged
	}
	if fields != 1 {
		return openapi.UpdateEntry400JSONResponse(errorBody(CodeOneFieldPerUpdate,
			"Set exactly one of level, locationId, purchasedOn or expiresOn.")), nil
	}
	if body.Level != nil && !validLevel(*body.Level) {
		return openapi.UpdateEntry400JSONResponse(invalidLevelBody()), nil
	}
	householdID := pgUUID(req.HouseholdId)
	entryID := pgUUID(req.EntryId)

	var result entry
	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if _, err := getMembership(ctx, q, req.HouseholdId, userID); err != nil {
			return err
		}
		// Lock the entry first: a concurrent request for it, including a
		// retry of this one, has then committed its event before the lookup
		// below.
		row, err := q.LockEntry(ctx, db.LockEntryParams{ID: entryID, HouseholdID: householdID})
		if errors.Is(err, pgx.ErrNoRows) {
			return errEntryNotFound
		}
		if err != nil {
			return err
		}
		old := entry(row)

		ev, found, err := lookupEvent(ctx, q, eventID)
		if err != nil {
			return err
		}
		if found {
			if ev.HouseholdID != householdID || ev.EntryID != entryID || ev.Type != eventType {
				return errEventIDConflict
			}
			// Already applied: return the entry as it is now.
			result = old
			return nil
		}

		next := old
		var changes events.Changes
		switch eventType {
		case db.EventTypeLevelChanged:
			next.Level = db.StockLevel(*body.Level)
			changes = events.Changes{events.ColumnLevel: {From: string(old.Level), To: string(next.Level)}}
		case db.EventTypeLocationChanged:
			if next.LocationID, err = checkLocation(ctx, q, *body.LocationId); err != nil {
				return err
			}
			changes = events.Changes{events.ColumnLocationID: {From: int(old.LocationID), To: int(next.LocationID)}}
		case db.EventTypePurchaseDateChanged:
			next.PurchasedOn = pgDate(body.PurchasedOn.Time)
			changes = events.Changes{events.ColumnPurchasedOn: {From: dateValue(old.PurchasedOn), To: dateValue(next.PurchasedOn)}}
		case db.EventTypeExpiryChanged:
			next.ExpiresOn = pgtype.Date{}
			if !body.ExpiresOn.IsNull() {
				next.ExpiresOn = pgDate(body.ExpiresOn.MustGet().Time)
			}
			changes = events.Changes{events.ColumnExpiresOn: {From: dateValue(old.ExpiresOn), To: dateValue(next.ExpiresOn)}}
		}
		if next.Level == old.Level && next.LocationID == old.LocationID &&
			sameDate(next.PurchasedOn, old.PurchasedOn) && sameDate(next.ExpiresOn, old.ExpiresOn) {
			// No change: no event, updated_at stays.
			result = old
			return nil
		}

		updated, err := q.UpdateEntry(ctx, db.UpdateEntryParams{
			ID:          entryID,
			HouseholdID: householdID,
			Level:       next.Level,
			LocationID:  next.LocationID,
			PurchasedOn: next.PurchasedOn,
			ExpiresOn:   next.ExpiresOn,
			ActorID:     userID,
		})
		if err != nil {
			return err
		}
		result = writtenEntry(updated, old.Name)
		return insertEvent(ctx, q, eventParams{ID: eventID, Actor: userID, Type: eventType, Changes: changes, Entry: result})
	})
	switch {
	case errors.Is(err, errHouseholdNotFound), errors.Is(err, errEntryNotFound):
		return openapi.UpdateEntry404JSONResponse{EntryNotFoundJSONResponse: entryNotFoundBody()}, nil
	case errors.Is(err, errInvalidLocation):
		return openapi.UpdateEntry400JSONResponse(invalidLocationBody()), nil
	case errors.Is(err, errEventIDConflict):
		return openapi.UpdateEntry409JSONResponse{EntryConflictJSONResponse: eventIDConflictBody()}, nil
	case err != nil:
		return nil, fmt.Errorf("PATCH /households/{id}/entries/{entryId}: %w", err)
	}
	return openapi.UpdateEntry200JSONResponse(result.response()), nil
}

// DeleteEntry deletes an entry and records its entry_deleted event.
func (a *API) DeleteEntry(ctx context.Context, req openapi.DeleteEntryRequestObject) (openapi.DeleteEntryResponseObject, error) {
	userID, ok := auth.UserID(ctx)
	if !ok {
		return nil, errNoUserInContext
	}
	eventID, ok := ParseUUIDv7(req.Params.EventId)
	if !ok {
		return openapi.DeleteEntry400JSONResponse(errorBody(CodeInvalidID, "eventId must be a UUIDv7.")), nil
	}
	householdID := pgUUID(req.HouseholdId)
	entryID := pgUUID(req.EntryId)

	err := dbtx.WithUser(ctx, a.db, userID, func(tx pgx.Tx) error {
		q := db.New(tx)
		if _, err := getMembership(ctx, q, req.HouseholdId, userID); err != nil {
			return err
		}
		// Lock first, as in UpdateEntry, so a concurrent retry of this delete
		// has committed its event before the lookup below.
		row, lockErr := q.LockEntry(ctx, db.LockEntryParams{ID: entryID, HouseholdID: householdID})
		if lockErr != nil && !errors.Is(lockErr, pgx.ErrNoRows) {
			return lockErr
		}
		ev, found, err := lookupEvent(ctx, q, eventID)
		if err != nil {
			return err
		}
		if found {
			if ev.HouseholdID != householdID || ev.EntryID != entryID || ev.Type != db.EventTypeEntryDeleted {
				return errEventIDConflict
			}
			return errEntryAlreadyDeleted
		}
		if lockErr != nil {
			return errEntryNotFound
		}
		old := entry(row)
		if _, err := q.DeleteEntry(ctx, db.DeleteEntryParams{ID: entryID, HouseholdID: householdID}); err != nil {
			return err
		}
		return insertEvent(ctx, q, eventParams{ID: eventID, Actor: userID, Type: db.EventTypeEntryDeleted, Entry: old})
	})
	switch {
	case errors.Is(err, errEntryAlreadyDeleted):
		return openapi.DeleteEntry204Response{}, nil
	case errors.Is(err, errHouseholdNotFound), errors.Is(err, errEntryNotFound):
		return openapi.DeleteEntry404JSONResponse{EntryNotFoundJSONResponse: entryNotFoundBody()}, nil
	case errors.Is(err, errEventIDConflict):
		return openapi.DeleteEntry409JSONResponse{EntryConflictJSONResponse: eventIDConflictBody()}, nil
	case err != nil:
		return nil, fmt.Errorf("DELETE /households/{id}/entries/{entryId}: %w", err)
	}
	return openapi.DeleteEntry204Response{}, nil
}

func invalidLevelBody() openapi.Error {
	return errorBody(CodeInvalidLevel, "level must be one of full, half, low or out.")
}

func invalidLocationBody() openapi.Error {
	return errorBody(CodeInvalidLocation, "locationId must be the ID of a location.")
}

func entryNotFoundBody() openapi.EntryNotFoundJSONResponse {
	return openapi.EntryNotFoundJSONResponse(errorBody(CodeNotFound, "Entry not found."))
}

func eventIDConflictBody() openapi.EntryConflictJSONResponse {
	return openapi.EntryConflictJSONResponse(errorBody(CodeEventIDConflict,
		"eventId was already used for a different change. Generate a new eventId."))
}

func entryIDConflictBody() openapi.EntryConflictJSONResponse {
	return openapi.EntryConflictJSONResponse(errorBody(CodeEntryIDConflict,
		"id is already used by another entry. Generate a new id."))
}

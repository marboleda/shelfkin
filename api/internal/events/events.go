// Package events defines the JSON shapes stored in item_events.changes and
// item_events.snapshot. It is the one place that documents them: the entry
// write endpoints (#19) write them, and reading the pantry's "last changed"
// label (#20) and a future activity feed read them back with the same types.
//
// Keys are the pantry_entries column names (snake_case), not the API's
// camelCase field names, because they describe database state. Dates are
// "YYYY-MM-DD" strings and location IDs are numbers.
package events

import (
	"encoding/json"
	"time"
)

// DateLayout is the format of every date in a snapshot or a change.
const DateLayout = time.DateOnly

// Snapshot is item_events.snapshot: the entry as it is after the event, or
// for entry_deleted, as it was just before the deletion. It carries the
// catalog item's name so a feed needs no joins and still works after the
// entry (or the item) is deleted.
type Snapshot struct {
	// Name is the catalog item's name at the time of the event.
	Name          string `json:"name"`
	CatalogItemID string `json:"catalog_item_id"`
	// Level is a stock_level: full, half, low or out.
	Level      string `json:"level"`
	LocationID int    `json:"location_id"`
	// PurchasedOn is a date in DateLayout.
	PurchasedOn string `json:"purchased_on"`
	// ExpiresOn is a date in DateLayout, or nil (JSON null) for no expiry.
	ExpiresOn *string `json:"expires_on"`
}

// Change is the old and new value of one column. From and To are a string
// (level, or a date in DateLayout), a number (location_id) or nil (JSON
// null, for expires_on when there is no expiry).
type Change struct {
	From any `json:"from"`
	To   any `json:"to"`
}

// Changes is item_events.changes, keyed by column name: level, location_id,
// purchased_on or expires_on. It is empty ({}) for entry_created and
// entry_deleted, and has exactly one key for every other event type:
//
//	level_changed          {"level": {"from": "half", "to": "low"}}
//	location_changed       {"location_id": {"from": 1, "to": 3}}
//	purchase_date_changed  {"purchased_on": {"from": "2026-10-20", "to": "2026-10-21"}}
//	expiry_changed         {"expires_on": {"from": "2026-10-27", "to": null}}
type Changes map[string]Change

// Column names used as keys of Changes.
const (
	ColumnLevel       = "level"
	ColumnLocationID  = "location_id"
	ColumnPurchasedOn = "purchased_on"
	ColumnExpiresOn   = "expires_on"
)

// Marshal returns the JSON of c, with nil marshalled as {} (never null).
func (c Changes) Marshal() ([]byte, error) {
	if c == nil {
		c = Changes{}
	}
	return json.Marshal(map[string]Change(c))
}

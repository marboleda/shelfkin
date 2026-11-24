// Sections and filters for the Pantry list, worked out from the pantry,
// locations and categories already loaded (no request). Pure functions with no
// React, so the offline cache (#35) and the expiring view (#110) can reuse them.
//
// A row is a pantry group. By location, a group is split into one row per
// location it has entries in, each row keeping only that location's entries,
// so "×N", the summary and the highlight follow what the row shows.

import type { Category, Location, PantryGroup } from './pantry';

export type ViewMode = 'location' | 'category';

export const VIEW_MODES: readonly ViewMode[] = ['location', 'category'];

/**
 * The active filters, as location and category IDs. Several of one kind match
 * any of them; a location and a category must both match (the #20 rule).
 */
export type PantryFilters = {
  locationIds: readonly number[];
  categoryIds: readonly number[];
};

export const NO_FILTERS: PantryFilters = { locationIds: [], categoryIds: [] };

export type PantrySection = {
  /** Unique within one view, e.g. `location-2`, `category-5` or `unknown`. */
  key: string;
  /** The location or category name, or "Unknown". */
  title: string;
  /** The rows, in the API's order (by name), each with only the entries shown in this section. */
  data: PantryGroup[];
};

export const UNKNOWN_SECTION_KEY = 'unknown';
export const UNKNOWN_SECTION_TITLE = 'Unknown';

export function locationSectionKey(locationId: number): string {
  return `location-${locationId}`;
}

export function categorySectionKey(categoryId: number): string {
  return `category-${categoryId}`;
}

/** The key of one row, unique across the whole list: one item can appear in several sections. */
export function rowKey(sectionKey: string, catalogItemId: string): string {
  return `${sectionKey}:${catalogItemId}`;
}

/** How many filters are on, for "Filters · 2". */
export function activeFilterCount(filters: PantryFilters): number {
  return filters.locationIds.length + filters.categoryIds.length;
}

/**
 * The filters with any ID that is not in the loaded lists dropped, in chip
 * order (the lists' order) and without duplicates.
 */
export function knownFilters(
  filters: PantryFilters,
  locations: readonly Location[],
  categories: readonly Category[],
): PantryFilters {
  const locationIds = new Set(filters.locationIds);
  const categoryIds = new Set(filters.categoryIds);
  return {
    locationIds: locations.filter((location) => locationIds.has(location.id)).map((location) => location.id),
    categoryIds: categories.filter((category) => categoryIds.has(category.id)).map((category) => category.id),
  };
}

/** The names of the active filters, locations first, in chip order: ["Fridge", "Dairy"]. */
export function filterNames(
  filters: PantryFilters,
  locations: readonly Location[],
  categories: readonly Category[],
): string[] {
  const locationIds = new Set(filters.locationIds);
  const categoryIds = new Set(filters.categoryIds);
  return [
    ...locations.filter((location) => locationIds.has(location.id)).map((location) => location.name),
    ...categories.filter((category) => categoryIds.has(category.id)).map((category) => category.name),
  ];
}

/** `ids` with `id` added, or removed if it was there. */
export function toggleId(ids: readonly number[], id: number): number[] {
  return ids.includes(id) ? ids.filter((other) => other !== id) : [...ids, id];
}

/**
 * The groups the filters keep. A category filter keeps or drops whole groups
 * by `categoryId`; a location filter keeps only the matching entries of each
 * group and drops groups left with none. Groups and entries keep their order.
 */
export function filterGroups(groups: readonly PantryGroup[], filters: PantryFilters): PantryGroup[] {
  const categoryIds = filters.categoryIds.length > 0 ? new Set(filters.categoryIds) : null;
  const locationIds = filters.locationIds.length > 0 ? new Set(filters.locationIds) : null;
  if (!categoryIds && !locationIds) return [...groups];

  const kept: PantryGroup[] = [];
  for (const group of groups) {
    if (categoryIds && !categoryIds.has(group.categoryId)) continue;
    if (!locationIds) {
      kept.push(group);
      continue;
    }
    const entries = group.entries.filter((entry) => locationIds.has(entry.locationId));
    if (entries.length === 0) continue;
    kept.push(entries.length === group.entries.length ? group : { ...group, entries });
  }
  return kept;
}

/**
 * One section per location with at least one entry, in the locations' order,
 * then "Unknown" for entries whose location is not in the list. A group with
 * entries in several locations gives a row in each, with only that location's
 * entries.
 */
export function sectionsByLocation(groups: readonly PantryGroup[], locations: readonly Location[]): PantrySection[] {
  const known = new Set(locations.map((location) => location.id));
  const rows = new Map<number | typeof UNKNOWN_SECTION_KEY, PantryGroup[]>();

  for (const group of groups) {
    // This group's entries by location, keeping the API's entry order.
    const byLocation = new Map<number | typeof UNKNOWN_SECTION_KEY, PantryGroup['entries']>();
    for (const entry of group.entries) {
      const bucket = known.has(entry.locationId) ? entry.locationId : UNKNOWN_SECTION_KEY;
      const entries = byLocation.get(bucket);
      if (entries) entries.push(entry);
      else byLocation.set(bucket, [entry]);
    }
    for (const [bucket, entries] of byLocation) {
      const row = entries.length === group.entries.length ? group : { ...group, entries };
      const sectionRows = rows.get(bucket);
      if (sectionRows) sectionRows.push(row);
      else rows.set(bucket, [row]);
    }
  }

  const sections: PantrySection[] = [];
  for (const location of locations) {
    const data = rows.get(location.id);
    if (data) sections.push({ key: locationSectionKey(location.id), title: location.name, data });
  }
  const unknown = rows.get(UNKNOWN_SECTION_KEY);
  if (unknown) sections.push({ key: UNKNOWN_SECTION_KEY, title: UNKNOWN_SECTION_TITLE, data: unknown });
  return sections;
}

/**
 * One section per category with at least one group, in the categories' order,
 * then "Unknown". Each group appears once, under its (effective) `categoryId`.
 */
export function sectionsByCategory(groups: readonly PantryGroup[], categories: readonly Category[]): PantrySection[] {
  const known = new Set(categories.map((category) => category.id));
  const rows = new Map<number | typeof UNKNOWN_SECTION_KEY, PantryGroup[]>();

  for (const group of groups) {
    if (group.entries.length === 0) continue;
    const bucket = known.has(group.categoryId) ? group.categoryId : UNKNOWN_SECTION_KEY;
    const sectionRows = rows.get(bucket);
    if (sectionRows) sectionRows.push(group);
    else rows.set(bucket, [group]);
  }

  const sections: PantrySection[] = [];
  for (const category of categories) {
    const data = rows.get(category.id);
    if (data) sections.push({ key: categorySectionKey(category.id), title: category.name, data });
  }
  const unknown = rows.get(UNKNOWN_SECTION_KEY);
  if (unknown) sections.push({ key: UNKNOWN_SECTION_KEY, title: UNKNOWN_SECTION_TITLE, data: unknown });
  return sections;
}

/** The Pantry list's sections for a view mode and filters. Empty when nothing matches. */
export function buildSections(
  groups: readonly PantryGroup[],
  options: {
    mode: ViewMode;
    filters: PantryFilters;
    locations: readonly Location[];
    categories: readonly Category[];
  },
): PantrySection[] {
  const shown = filterGroups(groups, options.filters);
  return options.mode === 'category'
    ? sectionsByCategory(shown, options.categories)
    : sectionsByLocation(shown, options.locations);
}

/** Whether the pantry has no entries at all (the empty-pantry state, not "no matches"). */
export function hasNoEntries(groups: readonly PantryGroup[]): boolean {
  return groups.every((group) => group.entries.length === 0);
}

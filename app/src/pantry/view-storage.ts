// The Pantry view mode and filters each user last chose on this device. The key
// includes the user ID (as in src/household/storage.ts), so two people sharing a
// device each keep their own. The same choices apply in every household:
// location and category IDs are global.

import AsyncStorage from '@react-native-async-storage/async-storage';

import { NO_FILTERS, VIEW_MODES, type PantryFilters, type ViewMode } from './sections';

const KEY_PREFIX = 'shelfkin.pantryView.';

export type PantryViewChoices = {
  mode: ViewMode;
  filters: PantryFilters;
};

/** A new install, or saved data that cannot be read: by location, no filters. */
export const DEFAULT_PANTRY_VIEW: PantryViewChoices = { mode: 'location', filters: NO_FILTERS };

export function pantryViewKey(uid: string): string {
  return `${KEY_PREFIX}${uid}`;
}

function idList(value: unknown): number[] | null {
  if (!Array.isArray(value)) return null;
  if (!value.every((id) => typeof id === 'number' && Number.isInteger(id))) return null;
  return [...new Set(value as number[])];
}

/**
 * The choices in a saved value, or the defaults when it is missing or
 * malformed. IDs are not checked against the locations and categories here;
 * the screen ignores unknown ones (see `knownFilters`). A value saved before
 * the Expiring chip (#110) has no `expiring`, which reads as off.
 */
export function parsePantryView(raw: string | null): PantryViewChoices {
  if (raw === null) return DEFAULT_PANTRY_VIEW;
  let value: unknown;
  try {
    value = JSON.parse(raw);
  } catch {
    return DEFAULT_PANTRY_VIEW;
  }
  if (typeof value !== 'object' || value === null) return DEFAULT_PANTRY_VIEW;
  const { mode, locationIds, categoryIds, expiring = false } = value as Record<string, unknown>;
  const locations = idList(locationIds);
  const categories = idList(categoryIds);
  if (!VIEW_MODES.includes(mode as ViewMode) || !locations || !categories || typeof expiring !== 'boolean') {
    return DEFAULT_PANTRY_VIEW;
  }
  return { mode: mode as ViewMode, filters: { locationIds: locations, categoryIds: categories, expiring } };
}

export function serializePantryView(choices: PantryViewChoices): string {
  return JSON.stringify({
    mode: choices.mode,
    locationIds: choices.filters.locationIds,
    categoryIds: choices.filters.categoryIds,
    expiring: choices.filters.expiring,
  });
}

/** The user's saved choices, or the defaults when there are none or storage cannot be read. Never throws. */
export async function loadPantryView(uid: string): Promise<PantryViewChoices> {
  try {
    return parsePantryView(await AsyncStorage.getItem(pantryViewKey(uid)));
  } catch {
    return DEFAULT_PANTRY_VIEW;
  }
}

/** Saves the user's choices. Never throws: failing to save only loses them on the next start. */
export async function savePantryView(uid: string, choices: PantryViewChoices): Promise<void> {
  try {
    await AsyncStorage.setItem(pantryViewKey(uid), serializePantryView(choices));
  } catch (error) {
    console.warn('Could not remember the Pantry view', error);
  }
}

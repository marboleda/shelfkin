// Pure helpers that read and change one entry in a cached `Pantry`, for Entry
// detail and the optimistic updates of its mutations. None of them change the
// pantry they are given: each returns a new one, sharing what did not change.

import type { ExpiryStatus } from './format';
import { addDays } from './dates';
import type { Pantry, PantryGroup, PantryListEntry } from './pantry';

export type FoundEntry = { group: PantryGroup; entry: PantryListEntry };

/** The entry with this ID and the item group it is in, or null when the pantry has no such entry. */
export function findEntry(pantry: Pantry | undefined, entryId: string): FoundEntry | null {
  if (!pantry) return null;
  for (const group of pantry.groups) {
    const entry = group.entries.find((candidate) => candidate.id === entryId);
    if (entry) return { group, entry };
  }
  return null;
}

/** Returns the pantry with `change` applied to the entry, or the same pantry when it has no such entry. */
export function updateEntryIn(
  pantry: Pantry,
  entryId: string,
  change: (entry: PantryListEntry) => PantryListEntry,
): Pantry {
  let found = false;
  const groups = pantry.groups.map((group) => {
    if (!group.entries.some((entry) => entry.id === entryId)) return group;
    found = true;
    return { ...group, entries: group.entries.map((entry) => (entry.id === entryId ? change(entry) : entry)) };
  });
  return found ? { ...pantry, groups } : pantry;
}

/** Where a removed entry was, so it can be put back exactly there. */
export type RemovedEntry = {
  entry: PantryListEntry;
  /** The group as it was before the removal (with the entry). */
  group: PantryGroup;
  groupIndex: number;
  entryIndex: number;
};

/**
 * Removes the entry, and its group when it was the group's last entry ("Milk
 * ×2" becomes a single row; a single row disappears). Null when the pantry has
 * no such entry.
 */
export function removeEntryFrom(pantry: Pantry, entryId: string): { pantry: Pantry; removed: RemovedEntry } | null {
  for (let groupIndex = 0; groupIndex < pantry.groups.length; groupIndex += 1) {
    const group = pantry.groups[groupIndex];
    const entryIndex = group.entries.findIndex((entry) => entry.id === entryId);
    if (entryIndex === -1) continue;
    const entries = group.entries.filter((entry) => entry.id !== entryId);
    const groups =
      entries.length === 0
        ? pantry.groups.filter((_, index) => index !== groupIndex)
        : pantry.groups.map((candidate, index) => (index === groupIndex ? { ...group, entries } : candidate));
    return {
      pantry: { ...pantry, groups },
      removed: { entry: group.entries[entryIndex], group, groupIndex, entryIndex },
    };
  }
  return null;
}

/** Puts a removed entry back where it was. Does nothing when the pantry already has it again. */
export function restoreEntryIn(pantry: Pantry, removed: RemovedEntry): Pantry {
  if (findEntry(pantry, removed.entry.id)) return pantry;
  const groupIndex = pantry.groups.findIndex((group) => group.catalogItemId === removed.group.catalogItemId);
  if (groupIndex === -1) {
    const groups = [...pantry.groups];
    groups.splice(Math.min(removed.groupIndex, groups.length), 0, { ...removed.group, entries: [removed.entry] });
    return { ...pantry, groups };
  }
  const group = pantry.groups[groupIndex];
  const entries = [...group.entries];
  entries.splice(Math.min(removed.entryIndex, entries.length), 0, removed.entry);
  return { ...pantry, groups: pantry.groups.map((candidate, index) => (index === groupIndex ? { ...group, entries } : candidate)) };
}

/**
 * A stand-in for the API's `expiryStatus` while a change is on its way, using
 * the API's own rule (docs/data-model.md): `expired` before `today`, `soon`
 * from `today` to `today` + 3 days, null without an expiry or when out. The
 * pantry is refetched once the change is saved, and the API's value replaces it.
 */
export function provisionalExpiryStatus(entry: Pick<PantryListEntry, 'level' | 'expiresOn'>, today: string): ExpiryStatus {
  if (entry.level === 'out' || !entry.expiresOn) return null;
  if (entry.expiresOn < today) return 'expired';
  return entry.expiresOn <= addDays(today, 3) ? 'soon' : null;
}

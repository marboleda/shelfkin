// Pantry fixtures and handlers for tests that render Pantry.

import { http, HttpResponse } from 'msw';

import type { components } from '@/api/schema';

import { apiUrl } from './api';

type Pantry = components['schemas']['Pantry'];
type PantryGroup = components['schemas']['PantryGroup'];
type PantryListEntry = components['schemas']['PantryListEntry'];
type Location = components['schemas']['Location'];

/** The pantry's `today` in tests unless a test says otherwise. */
export const TEST_TODAY = '2026-11-02';

export const TEST_LOCATIONS: Location[] = [
  { id: 1, key: 'fridge', name: 'Fridge' },
  { id: 2, key: 'freezer', name: 'Freezer' },
  { id: 3, key: 'pantry', name: 'Pantry' },
];

let nextId = 1;

/** A UUID-shaped ID that is different on every call. */
export function testId(): string {
  const n = (nextId++).toString(16).padStart(12, '0');
  return `0190f3a1-7c2e-7b3d-9a4f-${n}`;
}

export function testEntry(overrides: Partial<PantryListEntry> = {}): PantryListEntry {
  return {
    id: testId(),
    catalogItemId: '0190f3a1-7c2e-7b3d-9a4f-ffffffffffff',
    locationId: 1,
    level: 'full',
    purchasedOn: '2026-10-20',
    expiresOn: null,
    createdAt: '2026-10-20T15:00:00Z',
    updatedAt: '2026-10-20T15:00:00Z',
    expiryStatus: null,
    lastChange: null,
    ...overrides,
  };
}

export function testGroup(name: string, entries: PantryListEntry[], overrides: Partial<PantryGroup> = {}): PantryGroup {
  const catalogItemId = overrides.catalogItemId ?? testId();
  return {
    catalogItemId,
    name,
    isCustom: false,
    categoryId: 1,
    entries: entries.map((entry) => ({ ...entry, catalogItemId })),
    ...overrides,
  };
}

export function testPantry(groups: PantryGroup[] = [], today = TEST_TODAY): Pantry {
  return { today, groups };
}

/** `GET /households/{id}/pantry` answering with `pantry` (or what `pantry` returns for the household). */
export function pantryHandler(pantry: Pantry | ((householdId: string) => Pantry) = testPantry()) {
  return http.get(apiUrl('/households/:householdId/pantry'), ({ params }) =>
    HttpResponse.json(typeof pantry === 'function' ? pantry(String(params.householdId)) : pantry),
  );
}

export function locationsHandler(locations: Location[] = TEST_LOCATIONS) {
  return http.get(apiUrl('/locations'), () => HttpResponse.json({ items: locations }));
}

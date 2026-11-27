// Pantry fixtures and handlers for tests that render Pantry.

import { http, HttpResponse } from 'msw';

import type { components } from '@/api/schema';
import { addDays } from '@/pantry/dates';

import { apiUrl } from './api';

type Pantry = components['schemas']['Pantry'];
type PantryGroup = components['schemas']['PantryGroup'];
type PantryListEntry = components['schemas']['PantryListEntry'];
type Location = components['schemas']['Location'];
type Category = components['schemas']['Category'];

/** The pantry's `today` in tests unless a test says otherwise. */
export const TEST_TODAY = '2026-11-02';

export const TEST_LOCATIONS: Location[] = [
  { id: 1, key: 'fridge', name: 'Fridge' },
  { id: 2, key: 'freezer', name: 'Freezer' },
  { id: 3, key: 'pantry', name: 'Pantry' },
];

export const TEST_CATEGORIES: Category[] = [
  { id: 1, key: 'produce', name: 'Produce' },
  { id: 2, key: 'dairy', name: 'Dairy' },
  { id: 3, key: 'meat_seafood', name: 'Meat & seafood' },
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

export function categoriesHandler(categories: Category[] = TEST_CATEGORIES) {
  return http.get(apiUrl('/categories'), () => HttpResponse.json({ items: categories }));
}

type UpdateEntry = components['schemas']['UpdateEntry'];

/** The API's `expiryStatus` rule: expired before `today`, soon up to `today` + 3 days, null when out or with no expiry. */
export function expiryStatusOn(entry: Pick<PantryListEntry, 'level' | 'expiresOn'>, today: string): PantryListEntry['expiryStatus'] {
  if (entry.level === 'out' || !entry.expiresOn) return null;
  if (entry.expiresOn < today) return 'expired';
  return entry.expiresOn <= addDays(today, 3) ? 'soon' : null;
}

export type RecordedUpdate = { householdId: string; entryId: string; body: UpdateEntry };
export type RecordedDelete = { householdId: string; entryId: string; eventId: string | null };

type Answer<T> = (request: T, call: number) => Response | undefined | Promise<Response | undefined>;

/**
 * A small in-memory pantry API for tests that change entries:
 * `GET /households/:id/pantry` returns `state.pantry`, `PATCH .../entries/:entryId`
 * applies the one field it is given (and sets `lastChange` to `actor`), and
 * `DELETE .../entries/:entryId` removes the entry. Each request is recorded, in
 * the order it arrived. `answerUpdate` and `answerDelete` can return another
 * response instead (an error, or a promise to answer later); returning
 * undefined applies the request as usual.
 */
export function fakePantryApi(
  initial: Pantry,
  options: {
    actor?: { id: string; name: string };
    answerUpdate?: Answer<RecordedUpdate>;
    answerDelete?: Answer<RecordedDelete>;
    answerPantry?: (call: number) => Response | undefined | Promise<Response | undefined>;
  } = {},
) {
  const state = {
    pantry: structuredClone(initial),
    updates: [] as RecordedUpdate[],
    deletes: [] as RecordedDelete[],
    pantryGets: 0,
    /** Requests that have arrived but not been answered yet. */
    inFlight: 0,
    /** The most requests that were ever waiting for an answer at the same time. */
    maxInFlight: 0,
    answerUpdate: options.answerUpdate,
    answerDelete: options.answerDelete,
    answerPantry: options.answerPantry,
  };
  const actor = options.actor ?? { id: '0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80', name: 'Sam' };

  function find(entryId: string) {
    for (const group of state.pantry.groups) {
      const entry = group.entries.find((candidate) => candidate.id === entryId);
      if (entry) return { group, entry };
    }
    return null;
  }

  async function track<T>(work: () => Promise<T>): Promise<T> {
    state.inFlight += 1;
    state.maxInFlight = Math.max(state.maxInFlight, state.inFlight);
    try {
      return await work();
    } finally {
      state.inFlight -= 1;
    }
  }

  const notFound = () => HttpResponse.json({ error: { code: 'entry_not_found', message: 'No such entry.' } }, { status: 404 });

  const handlers = [
    http.get(apiUrl('/households/:householdId/pantry'), async () => {
      state.pantryGets += 1;
      const custom = await state.answerPantry?.(state.pantryGets);
      if (custom) return custom;
      return HttpResponse.json(structuredClone(state.pantry));
    }),
    http.patch(apiUrl('/households/:householdId/entries/:entryId'), async ({ request, params }) =>
      track(async () => {
        const recorded: RecordedUpdate = {
          householdId: String(params.householdId),
          entryId: String(params.entryId),
          body: (await request.json()) as UpdateEntry,
        };
        state.updates.push(recorded);
        const custom = await state.answerUpdate?.(recorded, state.updates.length);
        if (custom) return custom;
        const found = find(recorded.entryId);
        if (!found) return notFound();
        const { eventId, ...fields } = recorded.body;
        void eventId;
        const [field, value] = Object.entries(fields)[0] ?? [];
        const column = { level: 'level', locationId: 'location_id', purchasedOn: 'purchased_on', expiresOn: 'expires_on' }[
          field as 'level'
        ];
        const type = {
          level: 'level_changed',
          locationId: 'location_changed',
          purchasedOn: 'purchase_date_changed',
          expiresOn: 'expiry_changed',
        }[field as 'level'] as NonNullable<PantryListEntry['lastChange']>['type'];
        const entry = found.entry as Record<string, unknown>;
        const from = entry[field];
        entry[field] = value;
        found.entry.expiryStatus = expiryStatusOn(found.entry, state.pantry.today);
        found.entry.lastChange = {
          type,
          changes: { [column]: { from, to: value } },
          actorId: actor.id,
          actorName: actor.name,
          occurredAt: new Date().toISOString(),
        };
        const { expiryStatus: _status, lastChange: _change, ...plain } = found.entry;
        void _status;
        void _change;
        return HttpResponse.json(plain);
      }),
    ),
    http.delete(apiUrl('/households/:householdId/entries/:entryId'), async ({ request, params }) =>
      track(async () => {
        const recorded: RecordedDelete = {
          householdId: String(params.householdId),
          entryId: String(params.entryId),
          eventId: new URL(request.url).searchParams.get('eventId'),
        };
        state.deletes.push(recorded);
        const custom = await state.answerDelete?.(recorded, state.deletes.length);
        if (custom) return custom;
        const found = find(recorded.entryId);
        if (!found) return notFound();
        found.group.entries = found.group.entries.filter((entry) => entry !== found.entry);
        state.pantry.groups = state.pantry.groups.filter((group) => group.entries.length > 0);
        return new HttpResponse(null, { status: 204 });
      }),
    ),
  ];

  return { state, handlers };
}

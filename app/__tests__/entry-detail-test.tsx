import type { QueryClient } from '@tanstack/react-query';
import { act, fireEvent, waitFor, within } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { HttpResponse } from 'msw';
import { Dimensions } from 'react-native';

import { queryKeys } from '@/api/query-keys';
import type { Pantry } from '@/pantry/pantry';
import { setupApiServer } from '../test-utils/api';
import { signedInApiHandlers, signInTestUser, testHousehold, testMe } from '../test-utils/auth';
import { apiError } from '../test-utils/catalog';
import { fakePantryApi, testEntry, testGroup, testPantry } from '../test-utils/pantry';

jest.setTimeout(180000);

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

// Retry without delay, and keep the app's QueryClient so a test can inspect the cache.
const mockCreatedClients: QueryClient[] = [];
jest.mock('@/api/query-client', () => {
  const actual = jest.requireActual('@/api/query-client');
  return {
    ...actual,
    createQueryClient: jest.fn(() => {
      const client = actual.createQueryClient({ retryDelay: 0, gcTime: Infinity });
      mockCreatedClients.push(client);
      return client;
    }),
  };
});

const APP_DIR = './src/app';
const UUID_V7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

const ME = testMe();
const SAM_ID = '0190f3a1-7c2e-7b3d-9a4f-00000000a001';
const HOME = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000a', name: 'Home', role: 'owner' });

const server = setupApiServer(...signedInApiHandlers(ME, [HOME]));

// Locations: 1 Fridge, 2 Freezer, 3 Pantry. The pantry's today is 2026-11-02.
const MILK_OLD = testEntry({
  level: 'low',
  locationId: 1,
  purchasedOn: '2026-10-20',
  expiresOn: '2026-11-03',
  expiryStatus: 'soon',
  lastChange: {
    type: 'level_changed',
    changes: { level: { from: 'half', to: 'low' } },
    actorId: SAM_ID,
    actorName: 'Alex',
    occurredAt: '2026-10-25T10:00:00Z',
  },
});
const MILK_NEW = testEntry({ level: 'full', locationId: 1, purchasedOn: '2026-10-27', expiresOn: '2026-11-10' });
const MILK = testGroup('Milk', [MILK_OLD, MILK_NEW]);
const BUTTER_ENTRY = testEntry({ level: 'half', locationId: 1, purchasedOn: '2026-10-12', expiresOn: null });
const BUTTER = testGroup('Butter', [BUTTER_ENTRY]);
const EGGS_ENTRY = testEntry({ level: 'out', locationId: 1, purchasedOn: '2026-10-01', expiresOn: null });
const EGGS = testGroup('Eggs', [EGGS_ENTRY]);

function initialPantry(): Pantry {
  return testPantry([BUTTER, EGGS, MILK]);
}

let api: ReturnType<typeof fakePantryApi>;

beforeEach(async () => {
  signInTestUser();
  await setWindowWidth(390);
  api = fakePantryApi(initialPantry(), { actor: { id: ME.id, name: ME.displayName } });
  server.use(...api.handlers);
});

async function setWindowWidth(width: number) {
  const metrics = { width, height: 900, scale: 1, fontScale: 1 };
  await act(async () => {
    Dimensions.set({ window: metrics, screen: metrics });
  });
}

function appQueryClient(): QueryClient {
  const client = mockCreatedClients[mockCreatedClients.length - 1];
  if (!client) throw new Error('The app has not created a QueryClient');
  return client;
}

function cachedPantry(): Pantry | undefined {
  return appQueryClient().getQueryData<Pantry>(queryKeys.households.pantry(HOME.id));
}

function cachedEntry(entryId: string) {
  for (const group of cachedPantry()?.groups ?? []) {
    const entry = group.entries.find((candidate) => candidate.id === entryId);
    if (entry) return entry;
  }
  return undefined;
}

async function press(element: Parameters<typeof fireEvent.press>[0]) {
  await act(async () => {
    fireEvent.press(element);
  });
}

async function openEntry(entryId: string) {
  await renderRouter(APP_DIR, { initialUrl: `/entries/${entryId}` });
  await screen.findByTestId('entry-detail');
}

function checked(testID: string): boolean {
  const element = screen.getByTestId(testID);
  return !!(element.props.accessibilityState?.checked ?? element.props['aria-checked']);
}

/** Chooses a date in a `DateField`'s native picker. */
async function chooseDate(field: string, date: string) {
  if (!screen.queryByTestId(`${field}-picker`)) await press(screen.getByTestId(field));
  const [year, month, day] = date.split('-').map(Number);
  await act(async () => {
    fireEvent(screen.getByTestId(`${field}-picker`), 'change', {
      nativeEvent: { timestamp: new Date(year, month - 1, day, 12).getTime(), utcOffset: 0 },
    });
  });
}

function deferred<T = void>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

/** Waits until every write has settled and the pantry refetch after it is done. */
async function waitForSaved() {
  await waitFor(() => expect(appQueryClient().isMutating()).toBe(0));
  await waitFor(() => expect(appQueryClient().isFetching()).toBe(0));
}

/** Removes an entry from the fake API's pantry, as if another member deleted it. */
function removeOnServer(entryId: string) {
  for (const group of api.state.pantry.groups) group.entries = group.entries.filter((entry) => entry.id !== entryId);
  api.state.pantry.groups = api.state.pantry.groups.filter((group) => group.entries.length > 0);
}

/** A row's test ID on Pantry: by location, Fridge is section `location-1`. */
function rowTestId(group: { catalogItemId: string }, sectionKey = 'location-1') {
  return `pantry-row-${sectionKey}:${group.catalogItemId}`;
}

describe('detail', () => {
  test('a direct visit loads the pantry and shows the entry', async () => {
    await openEntry(MILK_OLD.id);

    expect(screen.getByRole('heading', { name: 'Milk' })).toBeTruthy();
    expect(screen.getByTestId('entry-bought')).toHaveTextContent('Bought Oct 20 · Fridge');
    expect(screen.getByTestId('expiry-chip-soon')).toHaveTextContent('Expires tomorrow');
    expect(screen.getByTestId('last-change')).toHaveTextContent('Set to Low by Alex, Oct 25');
    expect(checked('entry-level-low')).toBe(true);
    expect(checked('entry-level-full')).toBe(false);
    expect(checked('entry-location-1')).toBe(true);
    expect(api.state.pantryGets).toBe(1);
  });

  test('an entry with no expiry says "No expiry"', async () => {
    await openEntry(BUTTER_ENTRY.id);
    expect(screen.getByTestId('expiry-plain')).toHaveTextContent('No expiry');
    expect(screen.queryByTestId('entry-no-expiry')).toBeNull();
  });

  test('opening an entry from Pantry uses the cached pantry, with no extra request', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/' });
    const row = await screen.findByTestId(rowTestId(BUTTER));
    expect(api.state.pantryGets).toBe(1);

    await press(within(row).getByRole('link'));
    await screen.findByTestId('entry-detail');
    expect(screen).toHavePathname(`/entries/${BUTTER_ENTRY.id}`);
    expect(screen.getByRole('heading', { name: 'Butter' })).toBeTruthy();
    expect(api.state.pantryGets).toBe(1);
  });

  test('an unknown ID says the item is not in the household any more, with a link back to Pantry', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/entries/0190f3a1-7c2e-7b3d-9a4f-0000deadbeef' });
    const missing = await screen.findByTestId('entry-missing');
    expect(missing).toHaveTextContent(/This item isn't in Home any more/);

    await press(screen.getByTestId('entry-missing-back'));
    await waitFor(() => expect(screen).toHavePathname('/'));
  });
});

describe('level', () => {
  test('a tap shows the new level at once and sends one PATCH with the level and a new eventId', async () => {
    const answer = deferred();
    api.state.answerUpdate = async () => {
      await answer.promise;
      return undefined;
    };
    await openEntry(MILK_OLD.id);

    await press(screen.getByTestId('entry-level-half'));

    // Before the API answers: Entry detail and the cached pantry (which Pantry shows) have changed.
    await waitFor(() => expect(checked('entry-level-half')).toBe(true));
    expect(checked('entry-level-low')).toBe(false);
    expect(screen.getByTestId('last-change')).toHaveTextContent('Set to Half by you, just now');
    expect(cachedEntry(MILK_OLD.id)?.level).toBe('half');

    await waitFor(() => expect(api.state.updates).toHaveLength(1));
    const { body, entryId, householdId } = api.state.updates[0];
    expect(householdId).toBe(HOME.id);
    expect(entryId).toBe(MILK_OLD.id);
    expect(Object.keys(body).sort()).toEqual(['eventId', 'level']);
    expect(body.level).toBe('half');
    expect(body.eventId).toMatch(UUID_V7);

    answer.resolve();
    await waitForSaved();
    // Refetched after the change, so the label now comes from the server.
    expect(api.state.pantryGets).toBe(2);
    expect(checked('entry-level-half')).toBe(true);
    expect(screen.getByTestId('last-change')).toHaveTextContent('Set to Half by you, just now');
  });

  test('tapping the level that is already selected sends nothing', async () => {
    await openEntry(MILK_OLD.id);
    await press(screen.getByTestId('entry-level-low'));
    await waitForSaved();
    expect(api.state.updates).toHaveLength(0);
  });

  test('a 500 rolls back to the previous level and says so', async () => {
    const answer = deferred();
    api.state.answerUpdate = async () => {
      await answer.promise;
      return apiError(500, 'internal');
    };
    await openEntry(MILK_OLD.id);

    await press(screen.getByTestId('entry-level-full'));
    await waitFor(() => expect(checked('entry-level-full')).toBe(true));
    answer.resolve();

    await waitFor(() => expect(screen.getByTestId('household-notice')).toHaveTextContent(/Couldn't update Milk\. Try again\./));
    expect(checked('entry-level-low')).toBe(true);
    expect(checked('entry-level-full')).toBe(false);
    expect(cachedEntry(MILK_OLD.id)?.level).toBe('low');
    expect(screen.getByTestId('last-change')).toHaveTextContent('Set to Low by Alex, Oct 25');
    // Retried twice like a query, always with the same eventId.
    expect(api.state.updates).toHaveLength(3);
    expect(new Set(api.state.updates.map((update) => update.body.eventId)).size).toBe(1);
  });

  test('a retry after a network error reuses the eventId, and a change that then succeeds is kept', async () => {
    api.state.answerUpdate = (_request, call) => (call === 1 ? HttpResponse.error() : undefined);
    await openEntry(MILK_OLD.id);

    await press(screen.getByTestId('entry-level-out'));
    await waitForSaved();

    expect(api.state.updates).toHaveLength(2);
    expect(api.state.updates[1].body.eventId).toBe(api.state.updates[0].body.eventId);
    expect(checked('entry-level-out')).toBe(true);
    expect(screen.queryByTestId('household-notice')).toBeNull();
  });

  test('Full, Half, Low tapped quickly are sent one at a time, in order, and end on Low', async () => {
    const answers = [deferred(), deferred(), deferred()];
    api.state.answerUpdate = async (_request, call) => {
      await answers[call - 1].promise;
      return undefined;
    };
    await openEntry(EGGS_ENTRY.id);

    for (const level of ['full', 'half', 'low']) {
      await press(screen.getByTestId(`entry-level-${level}`));
      await waitFor(() => expect(checked(`entry-level-${level}`)).toBe(true));
    }

    // Only the first is sent until it is answered.
    await waitFor(() => expect(api.state.updates).toHaveLength(1));
    answers[0].resolve();
    await waitFor(() => expect(api.state.updates).toHaveLength(2));
    // The answer to Full never puts Full back on screen while Half and Low are pending.
    expect(checked('entry-level-low')).toBe(true);
    answers[1].resolve();
    await waitFor(() => expect(api.state.updates).toHaveLength(3));
    answers[2].resolve();
    await waitForSaved();

    expect(api.state.updates.map((update) => update.body.level)).toEqual(['full', 'half', 'low']);
    expect(api.state.maxInFlight).toBe(1);
    expect(checked('entry-level-low')).toBe(true);
    expect(api.state.pantry.groups[1].entries[0].level).toBe('low');
    // Refetched once, after the last change.
    expect(api.state.pantryGets).toBe(2);
  });

  test('setting Out keeps the entry on Pantry', async () => {
    await openEntry(BUTTER_ENTRY.id);
    await press(screen.getByTestId('entry-level-out'));
    await waitForSaved();

    await act(async () => {
      fireEvent.press(screen.getByRole('link', { name: 'Back to Pantry' }));
    });
    const row = await screen.findByTestId(rowTestId(BUTTER));
    expect(within(row).getByText('Out')).toBeTruthy();
  });

  test('a background refetch in flight does not flip the level back', async () => {
    await openEntry(MILK_OLD.id);
    // A pantry refetch is on its way, and will answer with the level before the change.
    const stale = structuredClone(api.state.pantry);
    const refetchAnswer = deferred();
    api.state.answerPantry = async (call) => {
      if (call !== 2) return undefined;
      await refetchAnswer.promise;
      return HttpResponse.json(stale);
    };
    await act(async () => {
      void appQueryClient().refetchQueries({ queryKey: queryKeys.households.pantry(HOME.id) });
    });
    await waitFor(() => expect(api.state.pantryGets).toBe(2));

    await press(screen.getByTestId('entry-level-full'));
    refetchAnswer.resolve();
    await waitForSaved();

    expect(checked('entry-level-full')).toBe(true);
    expect(cachedEntry(MILK_OLD.id)?.level).toBe('full');
  });
});

test('a pantry refetch that starts while a change is pending keeps the change on screen', async () => {
  const answer = deferred();
  api.state.answerUpdate = async () => {
    await answer.promise;
    return undefined;
  };
  await openEntry(MILK_OLD.id);
  await press(screen.getByTestId('entry-level-full'));
  await waitFor(() => expect(checked('entry-level-full')).toBe(true));

  // The server still has Low: the change has not been answered yet.
  await act(async () => {
    await appQueryClient().refetchQueries({ queryKey: queryKeys.households.pantry(HOME.id) });
  });
  expect(api.state.pantryGets).toBe(2);
  expect(checked('entry-level-full')).toBe(true);

  answer.resolve();
  await waitForSaved();
  expect(checked('entry-level-full')).toBe(true);
});

describe('other fields', () => {
  test('a new location sends only locationId, and the entry moves to its new section on Pantry', async () => {
    await openEntry(BUTTER_ENTRY.id);

    await press(screen.getByTestId('entry-location-2'));
    await waitFor(() => expect(checked('entry-location-2')).toBe(true));
    expect(screen.getByTestId('entry-bought')).toHaveTextContent('Bought Oct 12 · Freezer');
    await waitForSaved();

    expect(api.state.updates).toHaveLength(1);
    expect(Object.keys(api.state.updates[0].body).sort()).toEqual(['eventId', 'locationId']);
    expect(api.state.updates[0].body.locationId).toBe(2);
    expect(screen.getByTestId('last-change')).toHaveTextContent('Moved to Freezer by you, just now');

    await press(screen.getByRole('link', { name: 'Back to Pantry' }));
    expect(await screen.findByTestId(rowTestId(BUTTER, 'location-2'))).toBeTruthy();
    expect(screen.queryByTestId(rowTestId(BUTTER, 'location-1'))).toBeNull();
  });

  test('a new purchase date sends only purchasedOn and leaves the expiry as it is', async () => {
    await openEntry(MILK_OLD.id);
    await chooseDate('entry-bought-date', '2026-10-22');
    await waitForSaved();

    expect(api.state.updates).toHaveLength(1);
    expect(Object.keys(api.state.updates[0].body).sort()).toEqual(['eventId', 'purchasedOn']);
    expect(api.state.updates[0].body.purchasedOn).toBe('2026-10-22');
    expect(cachedEntry(MILK_OLD.id)?.expiresOn).toBe('2026-11-03');
  });

  test('a new expiry sends only expiresOn, and "No expiry" sends expiresOn: null', async () => {
    await openEntry(MILK_OLD.id);
    await chooseDate('entry-expires', '2026-11-20');
    await waitForSaved();
    expect(api.state.updates[0].body).toEqual({ eventId: expect.stringMatching(UUID_V7), expiresOn: '2026-11-20' });
    expect(screen.getByTestId('expiry-plain')).toHaveTextContent('Expires Nov 20');

    await press(screen.getByTestId('entry-no-expiry'));
    await waitFor(() => expect(screen.getByTestId('expiry-plain')).toHaveTextContent('No expiry'));
    await waitForSaved();

    expect(api.state.updates).toHaveLength(2);
    expect(api.state.updates[1].body).toEqual({ eventId: expect.stringMatching(UUID_V7), expiresOn: null });
    expect(api.state.updates[1].body.eventId).not.toBe(api.state.updates[0].body.eventId);
    expect(screen.getByTestId('last-change')).toHaveTextContent('Expiry removed by you, just now');
  });

  test('a 404 on update (deleted by someone else) removes the entry and says it is gone', async () => {
    await openEntry(BUTTER_ENTRY.id);
    // Someone else deletes it.
    removeOnServer(BUTTER_ENTRY.id);
    api.state.answerUpdate = () => apiError(404, 'entry_not_found');

    await press(screen.getByTestId('entry-level-low'));

    expect(await screen.findByTestId('entry-missing')).toHaveTextContent(/This item isn't in Home any more/);
    expect(cachedEntry(BUTTER_ENTRY.id)).toBeUndefined();
    expect(api.state.updates).toHaveLength(1);
  });
});

describe('delete', () => {
  test('asks first; Cancel changes nothing', async () => {
    await openEntry(MILK_OLD.id);
    await press(screen.getByTestId('entry-delete'));

    const dialog = await screen.findByTestId('entry-delete-dialog');
    expect(dialog).toHaveTextContent(/Delete Milk \(Oct 20\)\?/);
    expect(dialog).toHaveTextContent(/This can't be undone\./);

    await press(screen.getByTestId('entry-delete-dialog-cancel'));
    await waitFor(() => expect(screen.queryByTestId('entry-delete-dialog')).toBeNull());
    expect(api.state.deletes).toHaveLength(0);
    expect(screen).toHavePathname(`/entries/${MILK_OLD.id}`);
    expect(cachedEntry(MILK_OLD.id)).toBeDefined();
  });

  test('confirming sends DELETE with a new eventId, removes the entry and returns to Pantry', async () => {
    await openEntry(MILK_OLD.id);
    await press(screen.getByTestId('entry-delete'));
    await press(await screen.findByTestId('entry-delete-dialog-confirm'));

    await waitFor(() => expect(screen).toHavePathname('/'));
    await waitForSaved();
    expect(api.state.deletes).toHaveLength(1);
    expect(api.state.deletes[0].entryId).toBe(MILK_OLD.id);
    expect(api.state.deletes[0].eventId).toMatch(UUID_V7);
    expect(cachedEntry(MILK_OLD.id)).toBeUndefined();

    // "Milk ×2" is now a single-entry row.
    const row = await screen.findByTestId(rowTestId(MILK));
    expect(within(row).queryByTestId('pantry-row-count')).toBeNull();
  });

  test("deleting a group's last entry removes its row", async () => {
    await openEntry(BUTTER_ENTRY.id);
    await press(screen.getByTestId('entry-delete'));
    await press(await screen.findByTestId('entry-delete-dialog-confirm'));
    await waitFor(() => expect(screen).toHavePathname('/'));
    await waitForSaved();

    await screen.findByTestId(rowTestId(MILK));
    expect(screen.queryByTestId(rowTestId(BUTTER))).toBeNull();
    expect(cachedPantry()?.groups.map((group) => group.name)).toEqual(['Eggs', 'Milk']);
  });

  test('the entry is removed at once, before the API answers', async () => {
    const answer = deferred();
    api.state.answerDelete = async () => {
      await answer.promise;
      return undefined;
    };
    await openEntry(BUTTER_ENTRY.id);
    await press(screen.getByTestId('entry-delete'));
    await press(await screen.findByTestId('entry-delete-dialog-confirm'));

    await waitFor(() => expect(cachedEntry(BUTTER_ENTRY.id)).toBeUndefined());
    expect(screen).toHavePathname('/');
    answer.resolve();
    await waitForSaved();
  });

  test('a failed delete puts the entry back and says so', async () => {
    api.state.answerDelete = () => apiError(500, 'internal');
    await openEntry(MILK_OLD.id);
    await press(screen.getByTestId('entry-delete'));
    await press(await screen.findByTestId('entry-delete-dialog-confirm'));

    await waitFor(() => expect(screen.getByTestId('household-notice')).toHaveTextContent(/Couldn't delete Milk\. Try again\./));
    await waitForSaved();
    expect(cachedPantry()?.groups.find((group) => group.name === 'Milk')?.entries.map((entry) => entry.id)).toEqual([
      MILK_OLD.id,
      MILK_NEW.id,
    ]);
    expect(within(await screen.findByTestId(rowTestId(MILK))).getByTestId('pantry-row-count')).toHaveTextContent('×2');
  });

  test('a 404 counts as already deleted', async () => {
    await openEntry(MILK_OLD.id);
    removeOnServer(MILK_OLD.id);
    api.state.answerDelete = () => apiError(404, 'entry_not_found');
    await press(screen.getByTestId('entry-delete'));
    await press(await screen.findByTestId('entry-delete-dialog-confirm'));

    await waitFor(() => expect(screen.getByTestId('household-notice')).toHaveTextContent(/Milk was already removed/));
    expect(cachedEntry(MILK_OLD.id)).toBeUndefined();
    expect(api.state.deletes).toHaveLength(1);
  });
});

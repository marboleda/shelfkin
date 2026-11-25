// How Entry detail undoes a change that fails (#122): it goes straight to the
// latest change still on its way, or else to the last value the server
// accepted, never to an old value or to the value of another failed change.

import type { QueryClient } from '@tanstack/react-query';
import { act, fireEvent, waitFor } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';

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

const ME = testMe();
const HOME = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000a', name: 'Home', role: 'owner' });

const server = setupApiServer(...signedInApiHandlers(ME, [HOME]));

// The pantry's today is 2026-11-02, so an expiry on 2026-11-04 is "Expires in 2 days".
const MILK_ENTRY = testEntry({
  level: 'full',
  locationId: 1,
  purchasedOn: '2026-10-27',
  expiresOn: '2026-11-04',
  expiryStatus: 'soon',
  lastChange: {
    type: 'level_changed',
    changes: { level: { from: 'half', to: 'full' } },
    actorId: '0190f3a1-7c2e-7b3d-9a4f-00000000a001',
    actorName: 'Alex',
    occurredAt: '2026-10-25T10:00:00Z',
  },
});
const MILK = testGroup('Milk', [MILK_ENTRY]);

let api: ReturnType<typeof fakePantryApi>;
const cleanups: (() => void)[] = [];

beforeEach(() => {
  signInTestUser();
  api = fakePantryApi(testPantry([MILK]), { actor: { id: ME.id, name: ME.displayName } });
  server.use(...api.handlers);
});

afterEach(() => {
  while (cleanups.length) cleanups.pop()?.();
});

function appQueryClient(): QueryClient {
  const client = mockCreatedClients[mockCreatedClients.length - 1];
  if (!client) throw new Error('The app has not created a QueryClient');
  return client;
}

const pantryKey = () => queryKeys.households.pantry(HOME.id);

function cachedEntry() {
  const pantry = appQueryClient().getQueryData<Pantry>(pantryKey());
  return pantry?.groups.flatMap((group) => group.entries).find((entry) => entry.id === MILK_ENTRY.id);
}

function serverEntry() {
  return api.state.pantry.groups.flatMap((group) => group.entries).find((entry) => entry.id === MILK_ENTRY.id);
}

async function press(testID: string) {
  await act(async () => {
    fireEvent.press(screen.getByTestId(testID));
  });
}

async function openEntry() {
  await renderRouter(APP_DIR, { initialUrl: `/entries/${MILK_ENTRY.id}` });
  await screen.findByTestId('entry-detail');
}

function checked(testID: string): boolean {
  const element = screen.getByTestId(testID);
  return !!(element.props.accessibilityState?.checked ?? element.props['aria-checked']);
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

/**
 * Holds each change's PATCH until `release(n)` (n counts changes from 0, in
 * the order they were made), then answers it as `outcomes[n]` says. A failed
 * change answers 500 on every attempt, so its retries are used up.
 */
function controlUpdates(outcomes: ('ok' | 'fail')[]) {
  const gates = outcomes.map(() => deferred());
  const order: string[] = [];
  api.state.answerUpdate = async (request) => {
    const eventId = request.body.eventId;
    if (!order.includes(eventId)) order.push(eventId);
    const n = order.indexOf(eventId);
    await gates[n].promise;
    return outcomes[n] === 'fail' ? apiError(500, 'internal') : undefined;
  };
  return {
    release: async (n: number) => {
      await act(async () => {
        gates[n].resolve();
      });
    },
  };
}

/** Every level the cached pantry (which Entry detail and Pantry show) has shown the entry at, in order. */
function recordLevels() {
  const levels: string[] = [cachedEntry()?.level ?? 'none'];
  const unsubscribe = appQueryClient()
    .getQueryCache()
    .subscribe(() => {
      const level = cachedEntry()?.level ?? 'none';
      if (levels[levels.length - 1] !== level) levels.push(level);
    });
  cleanups.push(unsubscribe);
  return levels;
}

const notice = () => screen.queryByTestId('household-notice');

test('repro 1: when the first of Half, Low, Half fails, the level never shows Full and no notice is shown', async () => {
  const updates = controlUpdates(['fail', 'ok', 'ok']);
  await openEntry();
  const levels = recordLevels();

  for (const level of ['half', 'low', 'half']) {
    await press(`entry-level-${level}`);
    await waitFor(() => expect(checked(`entry-level-${level}`)).toBe(true));
  }
  await waitFor(() => expect(api.state.updates).toHaveLength(1));

  // Half (A) fails three times; Low and Half are still on their way.
  await updates.release(0);
  await waitFor(() => expect(api.state.updates).toHaveLength(4));
  expect(checked('entry-level-half')).toBe(true);
  expect(cachedEntry()?.level).toBe('half');
  expect(notice()).toBeNull();

  await updates.release(1);
  await waitFor(() => expect(api.state.updates).toHaveLength(5));
  await updates.release(2);
  await waitForSaved();

  expect(levels).toEqual(['full', 'half', 'low', 'half']);
  expect(checked('entry-level-half')).toBe(true);
  expect(serverEntry()?.level).toBe('half');
  expect(notice()).toBeNull();
});

test('repro 1: when the later change to the same field fails too, its own failure shows the notice', async () => {
  const updates = controlUpdates(['fail', 'fail']);
  await openEntry();

  await press('entry-level-half');
  await press('entry-level-low');
  await waitFor(() => expect(checked('entry-level-low')).toBe(true));
  await waitFor(() => expect(api.state.updates).toHaveLength(1));

  await updates.release(0);
  await waitFor(() => expect(api.state.updates).toHaveLength(4));
  expect(notice()).toBeNull();

  await updates.release(1);
  await waitFor(() => expect(notice()).toHaveTextContent(/Couldn't update Milk\. Try again\./));
  await waitForSaved();
});

test('repro 2: two failures in a row end on the server value, with one notice', async () => {
  const updates = controlUpdates(['fail', 'fail']);
  await openEntry();
  const levels = recordLevels();

  await press('entry-level-half');
  await press('entry-level-low');
  await waitFor(() => expect(checked('entry-level-low')).toBe(true));
  await waitFor(() => expect(api.state.updates).toHaveLength(1));

  // Half fails while Low is still on its way: Low stays, and no notice yet.
  await updates.release(0);
  await waitFor(() => expect(api.state.updates).toHaveLength(4));
  expect(checked('entry-level-low')).toBe(true);
  expect(notice()).toBeNull();

  // Low fails: straight back to Full (the server's value), never Half.
  await updates.release(1);
  await waitFor(() => expect(notice()).toHaveTextContent(/Couldn't update Milk\. Try again\./));
  expect(checked('entry-level-full')).toBe(true);
  expect(cachedEntry()?.level).toBe('full');
  expect(screen.getByTestId('last-change')).toHaveTextContent('Set to Full by Alex, Oct 25');
  await waitForSaved();

  expect(levels).toEqual(['full', 'half', 'low', 'full']);
  expect(serverEntry()?.level).toBe('full');
  expect(api.state.updates).toHaveLength(6);
});

test('a success then a failure ends on the successful value, with the notice', async () => {
  const updates = controlUpdates(['ok', 'fail']);
  await openEntry();
  const levels = recordLevels();

  await press('entry-level-half');
  await press('entry-level-low');
  await waitFor(() => expect(checked('entry-level-low')).toBe(true));

  await updates.release(0);
  await waitFor(() => expect(api.state.updates).toHaveLength(2));
  await updates.release(1);
  await waitFor(() => expect(notice()).toHaveTextContent(/Couldn't update Milk\. Try again\./));
  expect(checked('entry-level-half')).toBe(true);
  expect(screen.getByTestId('last-change')).toHaveTextContent('Set to Half by you, just now');
  await waitForSaved();

  expect(levels).toEqual(['full', 'half', 'low', 'half']);
  expect(checked('entry-level-half')).toBe(true);
  expect(serverEntry()?.level).toBe('half');
});

test('a single failure with nothing else pending goes back to the server value, with the notice', async () => {
  const updates = controlUpdates(['fail']);
  await openEntry();

  await press('entry-level-half');
  await waitFor(() => expect(checked('entry-level-half')).toBe(true));
  await updates.release(0);

  await waitFor(() => expect(notice()).toHaveTextContent(/Couldn't update Milk\. Try again\./));
  expect(checked('entry-level-full')).toBe(true);
  expect(screen.getByTestId('last-change')).toHaveTextContent('Set to Full by Alex, Oct 25');
  await waitForSaved();
});

describe('a failure of another field', () => {
  test('keeps a pending level change and its last-change label, and only the expiry goes back', async () => {
    const updates = controlUpdates(['fail', 'ok']);
    await openEntry();

    await press('entry-no-expiry');
    await waitFor(() => expect(screen.getByTestId('expiry-plain')).toHaveTextContent('No expiry'));
    await press('entry-level-low');
    await waitFor(() => expect(checked('entry-level-low')).toBe(true));
    await waitFor(() => expect(api.state.updates).toHaveLength(1));

    // The expiry change fails while Low is still on its way.
    await updates.release(0);
    await waitFor(() => expect(notice()).toHaveTextContent(/Couldn't update Milk\. Try again\./));
    expect(checked('entry-level-low')).toBe(true);
    expect(screen.getByTestId('last-change')).toHaveTextContent('Set to Low by you, just now');
    expect(screen.getByTestId('expiry-chip-soon')).toHaveTextContent('Expires in 2 days');
    expect(cachedEntry()?.expiresOn).toBe('2026-11-04');

    await updates.release(1);
    await waitForSaved();
    expect(checked('entry-level-low')).toBe(true);
    expect(screen.getByTestId('expiry-chip-soon')).toHaveTextContent('Expires in 2 days');
    expect(serverEntry()).toMatchObject({ level: 'low', expiresOn: '2026-11-04' });
  });

  test('keeps a level change that has already succeeded, and its last-change label', async () => {
    const updates = controlUpdates(['ok', 'fail']);
    await openEntry();

    await press('entry-level-low');
    await waitFor(() => expect(checked('entry-level-low')).toBe(true));
    await press('entry-no-expiry');
    await waitFor(() => expect(screen.getByTestId('expiry-plain')).toHaveTextContent('No expiry'));

    await updates.release(0);
    await waitFor(() => expect(api.state.updates).toHaveLength(2));
    await updates.release(1);
    await waitFor(() => expect(notice()).toHaveTextContent(/Couldn't update Milk\. Try again\./));
    expect(checked('entry-level-low')).toBe(true);
    expect(screen.getByTestId('last-change')).toHaveTextContent('Set to Low by you, just now');
    expect(screen.getByTestId('expiry-chip-soon')).toHaveTextContent('Expires in 2 days');
    await waitForSaved();
  });
});

test('a pantry fetch that answers while a change is starting does not become its rollback value', async () => {
  const updates = controlUpdates(['fail']);
  await openEntry();
  const fetched = structuredClone(api.state.pantry);

  // As soon as the change is pending (before its onMutate has read the pantry), a pantry fetch
  // answers with what the server has: Full. The pending change is applied again on top of it.
  let answered = false;
  const unsubscribe = appQueryClient()
    .getMutationCache()
    .subscribe((event) => {
      if (answered || event.type !== 'updated' || event.action.type !== 'pending') return;
      answered = true;
      appQueryClient().getQueryCache().find({ queryKey: pantryKey(), exact: true })?.setData(fetched);
    });
  cleanups.push(unsubscribe);

  await press('entry-level-half');
  await waitFor(() => expect(checked('entry-level-half')).toBe(true));
  expect(answered).toBe(true);

  await updates.release(0);
  await waitFor(() => expect(notice()).toHaveTextContent(/Couldn't update Milk\. Try again\./));
  expect(checked('entry-level-full')).toBe(true);
  expect(cachedEntry()?.level).toBe('full');
  await waitForSaved();
});

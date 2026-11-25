import type { QueryClient } from '@tanstack/react-query';
import { act, fireEvent, waitFor, within } from '@testing-library/react-native';
import { router } from 'expo-router';
import { renderRouter, screen } from 'expo-router/testing-library';
import { http, HttpResponse } from 'msw';
import { Dimensions } from 'react-native';

import { queryKeys } from '@/api/query-keys';
import type { CatalogItem } from '@/catalog/catalog';
import { isHouseholdItemPath } from '@/household/navigation';
import { apiUrl, setupApiServer } from '../test-utils/api';
import { signedInApiHandlers, signInTestUser, testHousehold, testMe } from '../test-utils/auth';
import { apiError, testCatalogItem } from '../test-utils/catalog';
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

// Locations: 1 Fridge, 2 Freezer, 3 Pantry. Categories: 1 Produce, 2 Dairy, 3 Meat & seafood.
const FRIDGE = 1;
const FREEZER = 2;
const PRODUCE = 1;
const DAIRY = 2;
const MEAT = 3;

const MILK_ID = '0190f3a1-7c2e-7b3d-9a4f-00000000b001';
const SALSA_ID = '0190f3a1-7c2e-7b3d-9a4f-00000000b002';

const MILK_ENTRY = testEntry({
  locationId: FRIDGE,
  lastChange: {
    type: 'level_changed',
    changes: { level: { from: 'full', to: 'half' } },
    actorId: ME.id,
    actorName: ME.displayName,
    occurredAt: '2026-10-25T10:00:00Z',
  },
});
const MILK_GROUP = testGroup('Milk', [MILK_ENTRY], { catalogItemId: MILK_ID, categoryId: DAIRY });

type Defaults = { categoryId: number; locationId: number };
type Recorded = { householdId: string; itemId: string; body: Record<string, unknown> };

/**
 * A small in-memory catalog-items API: `GET` returns the item, `PATCH` applies
 * its fields like the API does (for a built-in item, `null` or the built-in
 * default clears the override). A category change also moves the item's
 * group in the fake pantry. `answerPatch` can return another response.
 */
function fakeCatalogItemApi(items: { item: CatalogItem; builtIn?: Defaults }[], pantry: ReturnType<typeof fakePantryApi>) {
  const state = {
    items: new Map(items.map(({ item, builtIn }) => [item.id, { item: structuredClone(item), builtIn }])),
    patches: [] as Recorded[],
    gets: 0,
    answerPatch: undefined as ((request: Recorded, call: number) => Response | undefined | Promise<Response | undefined>) | undefined,
  };
  const notFound = () => apiError(404, 'catalog_item_not_found');
  const handlers = [
    http.get(apiUrl('/households/:householdId/catalog-items/:itemId'), ({ params }) => {
      state.gets += 1;
      const found = state.items.get(String(params.itemId));
      return found ? HttpResponse.json(found.item) : notFound();
    }),
    http.patch(apiUrl('/households/:householdId/catalog-items/:itemId'), async ({ request, params }) => {
      const recorded: Recorded = {
        householdId: String(params.householdId),
        itemId: String(params.itemId),
        body: (await request.json()) as Record<string, unknown>,
      };
      state.patches.push(recorded);
      const custom = await state.answerPatch?.(recorded, state.patches.length);
      if (custom) return custom;
      const found = state.items.get(recorded.itemId);
      if (!found) return notFound();
      const { item, builtIn } = found;
      for (const [field, overriddenField] of [
        ['categoryId', 'categoryOverridden'],
        ['locationId', 'locationOverridden'],
      ] as const) {
        if (!(field in recorded.body)) continue;
        const value = recorded.body[field] as number | null;
        if (builtIn) {
          const reset = value === null || value === builtIn[field];
          item[field] = reset ? builtIn[field] : value;
          item[overriddenField] = !reset;
        } else {
          if (value === null) return apiError(400, field === 'categoryId' ? 'invalid_category' : 'invalid_location');
          item[field] = value;
        }
      }
      if ('shelfLifeDays' in recorded.body) {
        if (builtIn) return apiError(400, 'not_overridable');
        item.shelfLifeDays = recorded.body.shelfLifeDays as number | null;
      }
      for (const group of pantry.state.pantry.groups) {
        if (group.catalogItemId === item.id) group.categoryId = item.categoryId;
      }
      return HttpResponse.json(item);
    }),
  ];
  return { state, handlers };
}

function milkItem(overrides: Partial<CatalogItem> = {}) {
  return testCatalogItem('Milk', { id: MILK_ID, categoryId: DAIRY, locationId: FRIDGE, shelfLifeDays: 7, ...overrides });
}

function salsaItem(overrides: Partial<CatalogItem> = {}) {
  return testCatalogItem('Salsa', {
    id: SALSA_ID,
    isCustom: true,
    categoryId: PRODUCE,
    locationId: FRIDGE,
    shelfLifeDays: null,
    ...overrides,
  });
}

let pantryApi: ReturnType<typeof fakePantryApi>;
let catalogApi: ReturnType<typeof fakeCatalogItemApi>;

beforeEach(async () => {
  signInTestUser();
  const metrics = { width: 390, height: 900, scale: 1, fontScale: 1 };
  await act(async () => {
    Dimensions.set({ window: metrics, screen: metrics });
  });
  pantryApi = fakePantryApi(testPantry([MILK_GROUP]));
  catalogApi = fakeCatalogItemApi(
    [{ item: milkItem(), builtIn: { categoryId: DAIRY, locationId: FRIDGE } }, { item: salsaItem() }],
    pantryApi,
  );
  server.use(...pantryApi.handlers, ...catalogApi.handlers);
});

function appQueryClient(): QueryClient {
  const client = mockCreatedClients[mockCreatedClients.length - 1];
  if (!client) throw new Error('The app has not created a QueryClient');
  return client;
}

async function press(element: Parameters<typeof fireEvent.press>[0]) {
  await act(async () => {
    fireEvent.press(element);
  });
}

function checked(testID: string): boolean {
  const element = screen.getByTestId(testID);
  return !!(element.props.accessibilityState?.checked ?? element.props['aria-checked']);
}

async function openItem(itemId: string) {
  await renderRouter(APP_DIR, { initialUrl: `/items/${itemId}` });
  await screen.findByTestId('item-settings');
  await screen.findByTestId('item-category-1');
  await screen.findByTestId('item-location-1');
}

async function waitForSaved() {
  await waitFor(() => expect(appQueryClient().isMutating()).toBe(0));
  await waitFor(() => expect(appQueryClient().isFetching()).toBe(0));
}

function deferred<T = void>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

async function typeShelfLife(text: string) {
  const field = screen.getByTestId('item-shelf-life');
  await act(async () => {
    fireEvent.changeText(field, text);
  });
  await act(async () => {
    fireEvent(field, 'submitEditing');
  });
}

describe('opening', () => {
  test('entry detail links to the item settings of its item', async () => {
    await renderRouter(APP_DIR, { initialUrl: `/entries/${MILK_ENTRY.id}` });
    await screen.findByTestId('entry-detail');
    expect(screen.getByRole('heading', { name: 'For all Milk' })).toBeTruthy();

    await press(screen.getByTestId('entry-item-settings'));
    await screen.findByTestId('item-settings');
    expect(screen).toHavePathname(`/items/${MILK_ID}`);
    expect(screen.getByRole('heading', { name: 'Milk' })).toBeTruthy();

    // The back link returns to the entry it was opened from.
    await press(screen.getByTestId('item-back'));
    await waitFor(() => expect(screen).toHavePathname(`/entries/${MILK_ENTRY.id}`));
  });

  test('a built-in item shows its kind, its defaults and where they come from, with no shelf life', async () => {
    await openItem(MILK_ID);

    expect(screen.getByTestId('item-kind')).toHaveTextContent('Shelfkin item');
    expect(checked(`item-category-${DAIRY}`)).toBe(true);
    expect(checked(`item-location-${FRIDGE}`)).toBe(true);
    expect(screen.getByTestId('item-category-source')).toHaveTextContent('Shelfkin default');
    expect(screen.getByTestId('item-location-source')).toHaveTextContent('Shelfkin default');
    expect(screen.queryByTestId('item-category-reset')).toBeNull();
    expect(screen.queryByTestId('item-shelf-life')).toBeNull();
    expect(screen.getByTestId('item-scope')).toHaveTextContent('Saved for Home and used the next time anyone adds Milk.');
  });

  test('a household override is labelled as the household setting', async () => {
    catalogApi.state.items.get(MILK_ID)!.item = milkItem({ locationId: FREEZER, locationOverridden: true });
    await openItem(MILK_ID);
    expect(checked(`item-location-${FREEZER}`)).toBe(true);
    expect(screen.getByTestId('item-location-source')).toHaveTextContent('Household setting');
    expect(screen.getByTestId('item-location-reset')).toBeTruthy();
    expect(screen.getByTestId('item-category-source')).toHaveTextContent('Shelfkin default');
  });

  test('a custom item says it is the household’s, with its shelf life and no sources or resets', async () => {
    catalogApi.state.items.get(SALSA_ID)!.item = salsaItem({ shelfLifeDays: 14 });
    await openItem(SALSA_ID);

    expect(screen.getByTestId('item-kind')).toHaveTextContent('Your household’s item');
    expect(screen.getByTestId('item-shelf-life').props.value).toBe('14');
    expect(screen.queryByTestId('item-category-source')).toBeNull();
    expect(screen.queryByTestId('item-category-reset')).toBeNull();
    expect(screen.queryByTestId('item-location-reset')).toBeNull();
  });

  test('a 404 says the item is not available, with a link back', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/items/0190f3a1-7c2e-7b3d-9a4f-0000deadbeef' });
    const missing = await screen.findByTestId('item-missing');
    expect(missing).toHaveTextContent(/This item isn't available/);

    await press(screen.getByTestId('item-missing-back'));
    await waitFor(() => expect(screen).toHavePathname('/'));
  });

  test('a malformed ID (400 invalid_id) says the item is not available too', async () => {
    server.use(http.get(apiUrl('/households/:householdId/catalog-items/:itemId'), () => apiError(400, 'invalid_id')));
    await renderRouter(APP_DIR, { initialUrl: '/items/not-a-real-id' });
    expect(await screen.findByTestId('item-missing')).toHaveTextContent(/This item isn't available/);
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();
  });

  test('a draft and the location note do not carry over to the next item', async () => {
    const kimchiId = '0190f3a1-7c2e-7b3d-9a4f-00000000b003';
    catalogApi.state.items.set(kimchiId, {
      item: salsaItem({ id: kimchiId, name: 'Kimchi', shelfLifeDays: 14 }),
      builtIn: undefined,
    });
    catalogApi.state.items.get(SALSA_ID)!.item = salsaItem({ shelfLifeDays: 14 });
    // Both items cached, so the second opens straight from the cache on the same mounted screen.
    await openItem(SALSA_ID);
    await act(async () => {
      router.navigate(`/items/${kimchiId}`);
    });
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Kimchi' })).toBeTruthy());

    await press(screen.getByTestId(`item-location-${FREEZER}`));
    await waitFor(() => expect(screen.getByTestId('item-location-note')).toBeTruthy());
    await typeShelfLife('abc');
    expect(screen.getByText(/Enter a whole number of days from 1 to 3650/)).toBeTruthy();
    await waitForSaved();

    await act(async () => {
      router.navigate(`/items/${SALSA_ID}`);
    });
    await waitFor(() => expect(screen.getByRole('heading', { name: 'Salsa' })).toBeTruthy());
    expect(screen.getByTestId('item-shelf-life').props.value).toBe('14');
    expect(screen.queryByText(/Enter a whole number of days/)).toBeNull();
    expect(screen.queryByTestId('item-location-note')).toBeNull();
  });

  test('another error can be retried', async () => {
    server.use(
      http.get(apiUrl('/households/:householdId/catalog-items/:itemId'), () => apiError(400, 'bad_request'), { once: true }),
    );
    await renderRouter(APP_DIR, { initialUrl: `/items/${MILK_ID}` });
    await screen.findByTestId('item-error');

    await press(screen.getByRole('button', { name: 'Retry' }));
    await screen.findByTestId('item-settings');
  });

  test('item settings is one of the screens left when the household changes', () => {
    expect(isHouseholdItemPath(`/items/${MILK_ID}`)).toBe(true);
  });
});

describe('built-in item', () => {
  test('overriding the category, resetting it, and choosing the built-in value', async () => {
    await openItem(MILK_ID);

    await press(screen.getByTestId(`item-category-${MEAT}`));
    await waitFor(() => expect(checked(`item-category-${MEAT}`)).toBe(true));
    await waitForSaved();
    expect(screen.getByTestId('item-category-source')).toHaveTextContent('Household setting');

    await press(screen.getByTestId('item-category-reset'));
    await waitForSaved();
    expect(checked(`item-category-${DAIRY}`)).toBe(true);
    expect(screen.getByTestId('item-category-source')).toHaveTextContent('Shelfkin default');
    expect(screen.queryByTestId('item-category-reset')).toBeNull();

    await press(screen.getByTestId(`item-category-${PRODUCE}`));
    await waitForSaved();
    await press(screen.getByTestId(`item-category-${DAIRY}`));
    await waitForSaved();
    expect(checked(`item-category-${DAIRY}`)).toBe(true);
    expect(screen.getByTestId('item-category-source')).toHaveTextContent('Shelfkin default');

    expect(catalogApi.state.patches.map((patch) => patch.body)).toEqual([
      { categoryId: MEAT },
      { categoryId: null },
      { categoryId: PRODUCE },
      { categoryId: DAIRY },
    ]);
    // Only the current household's path.
    expect(new Set(catalogApi.state.patches.map((patch) => patch.householdId))).toEqual(new Set([HOME.id]));
  });

  test('resetting an overridden location sends null and says Shelfkin default afterwards', async () => {
    catalogApi.state.items.get(MILK_ID)!.item = milkItem({ locationId: FREEZER, locationOverridden: true });
    await openItem(MILK_ID);

    await press(screen.getByTestId('item-location-reset'));
    await waitForSaved();
    expect(catalogApi.state.patches.map((patch) => patch.body)).toEqual([{ locationId: null }]);
    expect(checked(`item-location-${FRIDGE}`)).toBe(true);
    expect(screen.getByTestId('item-location-source')).toHaveTextContent('Shelfkin default');
  });

  test('choosing the selected value sends nothing', async () => {
    await openItem(MILK_ID);
    await press(screen.getByTestId(`item-category-${DAIRY}`));
    await waitForSaved();
    expect(catalogApi.state.patches).toHaveLength(0);
  });
});

describe('custom item', () => {
  test('changing the category and location', async () => {
    await openItem(SALSA_ID);

    await press(screen.getByTestId(`item-category-${DAIRY}`));
    await press(screen.getByTestId(`item-location-${FREEZER}`));
    await waitFor(() => expect(checked(`item-location-${FREEZER}`)).toBe(true));
    expect(checked(`item-category-${DAIRY}`)).toBe(true);
    expect(screen.getByTestId('item-location-note')).toHaveTextContent(
      'New Salsa entries will go to Freezer. Existing entries stay where they are.',
    );
    await waitForSaved();

    expect(catalogApi.state.patches.map((patch) => patch.body)).toEqual([{ categoryId: DAIRY }, { locationId: FREEZER }]);
    expect(checked(`item-category-${DAIRY}`)).toBe(true);
    expect(checked(`item-location-${FREEZER}`)).toBe(true);
    expect(catalogApi.state.items.get(SALSA_ID)!.item).toMatchObject({ categoryId: DAIRY, locationId: FREEZER });
  });

  test('setting and clearing the shelf life, with a message for other values', async () => {
    await openItem(SALSA_ID);

    await typeShelfLife('0');
    expect(screen.getByText(/Enter a whole number of days from 1 to 3650/)).toBeTruthy();
    await typeShelfLife('3651');
    expect(screen.getByText(/Enter a whole number of days from 1 to 3650/)).toBeTruthy();
    await typeShelfLife('2.5');
    expect(screen.getByText(/Enter a whole number of days from 1 to 3650/)).toBeTruthy();
    expect(catalogApi.state.patches).toHaveLength(0);

    await typeShelfLife('30');
    await waitForSaved();
    expect(screen.queryByText(/Enter a whole number of days/)).toBeNull();
    expect(screen.getByTestId('item-shelf-life').props.value).toBe('30');

    await typeShelfLife('');
    await waitForSaved();
    expect(screen.getByTestId('item-shelf-life').props.value).toBe('');

    await typeShelfLife('3650');
    await waitForSaved();
    await press(screen.getByTestId('item-shelf-life-clear'));
    await waitForSaved();
    expect(screen.getByTestId('item-shelf-life').props.value).toBe('');

    expect(catalogApi.state.patches.map((patch) => patch.body)).toEqual([
      { shelfLifeDays: 30 },
      { shelfLifeDays: null },
      { shelfLifeDays: 3650 },
      { shelfLifeDays: null },
    ]);
    expect(catalogApi.state.items.get(SALSA_ID)!.item.shelfLifeDays).toBeNull();
  });
});

describe('effects', () => {
  test('after saving, the household’s pantry and catalog search queries are invalidated', async () => {
    await openItem(MILK_ID);
    const client = appQueryClient();
    const searchKey = queryKeys.households.catalogSearch(HOME.id, 'mil');
    const pantryKey = queryKeys.households.pantry(HOME.id);
    client.setQueryData(searchKey, [milkItem()]);
    client.setQueryData(pantryKey, testPantry([MILK_GROUP]));
    expect(client.getQueryState(searchKey)?.isInvalidated).toBe(false);
    expect(client.getQueryState(pantryKey)?.isInvalidated).toBe(false);

    await press(screen.getByTestId(`item-location-${FREEZER}`));
    await waitFor(() => expect(checked(`item-location-${FREEZER}`)).toBe(true));
    expect(screen.getByTestId('item-location-note')).toHaveTextContent(
      'New Milk entries will go to Freezer. Existing entries stay where they are.',
    );
    await waitForSaved();

    expect(client.getQueryState(searchKey)?.isInvalidated).toBe(true);
    expect(client.getQueryState(pantryKey)?.isInvalidated).toBe(true);
    // The entry has not moved.
    expect(pantryApi.state.updates).toHaveLength(0);
  });

  test('after a category change, Pantry’s by-category view shows the item under the new category', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/' });
    await screen.findByTestId(`pantry-row-location-1:${MILK_ID}`);
    await press(screen.getByRole('radio', { name: 'Category' }));
    const row = await screen.findByTestId(`pantry-row-category-${DAIRY}:${MILK_ID}`);
    await press(within(row).getByText('Milk'));
    await screen.findByTestId('entry-detail');
    const lastChange = screen.getByTestId('last-change').props.children;

    await press(screen.getByTestId('entry-item-settings'));
    await screen.findByTestId(`item-category-${MEAT}`);
    await press(screen.getByTestId(`item-category-${MEAT}`));
    await waitForSaved();

    await press(screen.getByTestId('item-back'));
    await screen.findByTestId('entry-detail');
    // Defaults record no event: the entry's last change is the same.
    expect(screen.getByTestId('last-change').props.children).toEqual(lastChange);
    await press(screen.getByLabelText('Back to Pantry'));
    await screen.findByTestId(`pantry-row-category-${MEAT}:${MILK_ID}`);
    expect(screen.queryByTestId(`pantry-row-category-${DAIRY}:${MILK_ID}`)).toBeNull();
  });

  test('a failed save puts the old value back and says so', async () => {
    const answer = deferred();
    catalogApi.state.answerPatch = async () => {
      await answer.promise;
      return apiError(500, 'internal');
    };
    await openItem(MILK_ID);

    await press(screen.getByTestId(`item-category-${MEAT}`));
    await waitFor(() => expect(checked(`item-category-${MEAT}`)).toBe(true));
    expect(screen.getByTestId('item-category-saving')).toBeTruthy();
    answer.resolve();

    await waitFor(() => expect(screen.getByTestId('household-notice')).toHaveTextContent(/Couldn't update Milk\. Try again\./));
    expect(checked(`item-category-${DAIRY}`)).toBe(true);
    expect(checked(`item-category-${MEAT}`)).toBe(false);
    expect(screen.getByTestId('item-category-source')).toHaveTextContent('Shelfkin default');
    expect(screen.queryByTestId('item-category-saving')).toBeNull();
  });

  test('a failed shelf-life save puts the old value back', async () => {
    catalogApi.state.items.get(SALSA_ID)!.item = salsaItem({ shelfLifeDays: 14 });
    catalogApi.state.answerPatch = () => apiError(400, 'invalid_shelf_life');
    await openItem(SALSA_ID);

    await typeShelfLife('20');
    await waitFor(() =>
      expect(screen.getByTestId('household-notice')).toHaveTextContent(/That didn't work\. Check the details and try again\./),
    );
    expect(screen.getByTestId('item-shelf-life').props.value).toBe('14');
  });
});

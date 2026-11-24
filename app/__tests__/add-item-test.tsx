import type { QueryClient } from '@tanstack/react-query';
import { act, fireEvent, waitFor, within } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { http, HttpResponse } from 'msw';
import { Dimensions } from 'react-native';

import { queryKeys } from '@/api/query-keys';
import { SEARCH_DEBOUNCE_MS } from '@/components/add-item-search';
import { householdRowLabel } from '@/components/household-switcher';
import { addDays, todayOnDevice } from '@/pantry/dates';
import { apiUrl, setupApiServer } from '../test-utils/api';
import { signedInApiHandlers, signInTestUser, testHousehold, testMe } from '../test-utils/auth';
import {
  apiError,
  catalogSearchHandler,
  createCatalogItemHandler,
  createEntryHandler,
  searchIn,
  testCatalogItem,
} from '../test-utils/catalog';
import { pantryHandler, testEntry, testGroup, testPantry } from '../test-utils/pantry';

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

// Phones by default; the keyboard tests switch on the web's keyboard highlight.
jest.mock('@/components/add-item-keyboard', () => ({ usesKeyboardHighlight: jest.fn(() => false) }));
const mockUsesKeyboardHighlight = jest.requireMock('@/components/add-item-keyboard').usesKeyboardHighlight as jest.Mock;

// Records where keyboard focus is sent (it still calls the real focusElement).
jest.mock('@/components/focus', () => {
  const actual = jest.requireActual('@/components/focus');
  return { ...actual, focusElement: jest.fn(actual.focusElement) };
});
const mockFocusElement = jest.requireMock('@/components/focus').focusElement as jest.Mock;

/** The test ID of the element focus was last sent to. */
function lastFocused(): string | undefined {
  const calls = mockFocusElement.mock.calls;
  const element = calls[calls.length - 1]?.[0] as { props?: { testID?: string } } | null | undefined;
  return element?.props?.testID;
}

const APP_DIR = './src/app';
const UUID_V7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

const ME = testMe();
const HOME = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000a', name: 'Home', role: 'owner' });
const CABIN = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000b', name: 'Cabin', role: 'member' });

// Categories: 1 Produce, 2 Dairy, 3 Meat & seafood. Locations: 1 Fridge, 2 Freezer, 3 Pantry.
const MILK = testCatalogItem('Milk', { categoryId: 2, locationId: 1, shelfLifeDays: 7 });
const OAT_MILK = testCatalogItem('Oat milk', { categoryId: 2, locationId: 3, shelfLifeDays: null });
const FLOUR = testCatalogItem('Flour', { categoryId: 3, locationId: 3, shelfLifeDays: null });
const ICE_CREAM = testCatalogItem('Ice cream', { categoryId: 2, locationId: 2, shelfLifeDays: 60, locationOverridden: true });
const GOCHUJANG = testCatalogItem('Gochujang', { isCustom: true, categoryId: 1, locationId: 1, shelfLifeDays: 365 });
const CATALOG = [MILK, OAT_MILK, FLOUR, ICE_CREAM];

const server = setupApiServer(...signedInApiHandlers(ME, [HOME]));

let search: ReturnType<typeof catalogSearchHandler>;
let entries: ReturnType<typeof createEntryHandler>;

beforeEach(async () => {
  signInTestUser();
  await setWindowWidth(390);
  search = catalogSearchHandler(searchIn(CATALOG));
  entries = createEntryHandler();
  server.use(search.handler, entries.handler);
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

async function press(element: Parameters<typeof fireEvent.press>[0]) {
  await act(async () => {
    fireEvent.press(element);
  });
}

async function renderAdd() {
  await renderRouter(APP_DIR, { initialUrl: '/add' });
  await screen.findByTestId('add-item-name');
}

async function typeName(text: string) {
  await act(async () => {
    fireEvent.changeText(screen.getByTestId('add-item-name'), text);
  });
}

async function wait(ms: number) {
  await act(async () => {
    jest.advanceTimersByTime(ms);
  });
}

/** Types `text`, pauses long enough for the search, and waits for that search to finish. */
async function searchFor(text: string) {
  await typeName(text);
  await wait(SEARCH_DEBOUNCE_MS);
  await waitFor(() => expect(screen.queryByTestId('add-search-loading')).toBeNull());
}

/** Searches for `text` and picks the suggestion for `item`. */
async function pick(text: string, item: { id: string }) {
  await typeName(text);
  await wait(SEARCH_DEBOUNCE_MS);
  await press(await screen.findByTestId(`add-suggestion-${item.id}`));
  await screen.findByTestId('add-submit');
}

/** Searches for `text`, which has no match, and picks "Create". */
async function pickCreate(text: string) {
  await typeName(text);
  await wait(SEARCH_DEBOUNCE_MS);
  await press(await screen.findByTestId('add-create'));
  await screen.findByTestId('add-submit');
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

async function submit() {
  await press(screen.getByTestId('add-submit'));
}

/** Adds the item and waits for the form to come back empty. */
async function submitAndWaitForReset() {
  await submit();
  await screen.findByTestId('add-confirmation');
}

function lastEntry() {
  const last = entries.requests[entries.requests.length - 1];
  if (!last) throw new Error('No entry was created');
  return last.body;
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

describe('search', () => {
  test('typing "milk" quickly sends one request, after typing pauses', async () => {
    await renderAdd();
    for (const text of ['m', 'mi', 'mil', 'milk']) {
      await typeName(text);
      await wait(100);
    }
    expect(search.queries).toEqual([]);
    await wait(SEARCH_DEBOUNCE_MS);
    expect(await screen.findByTestId(`add-suggestion-${MILK.id}`)).toBeTruthy();
    expect(search.queries).toEqual(['milk']);
  });

  test('a slow response for older text never replaces newer results', async () => {
    const MINT = testCatalogItem('Mint', { categoryId: 1 });
    const slow = deferred<typeof MILK[]>();
    search = catalogSearchHandler((query) => (query === 'mi' ? slow.promise : [MILK]));
    server.use(search.handler);
    await renderAdd();

    await typeName('mi');
    await wait(SEARCH_DEBOUNCE_MS);
    await typeName('milk');
    await wait(SEARCH_DEBOUNCE_MS);
    expect(await screen.findByTestId(`add-suggestion-${MILK.id}`)).toBeTruthy();
    expect(search.queries).toEqual(['mi', 'milk']);

    await act(async () => {
      slow.resolve([MINT]);
    });
    await wait(50);
    expect(screen.queryByTestId(`add-suggestion-${MINT.id}`)).toBeNull();
    expect(screen.getByTestId(`add-suggestion-${MILK.id}`)).toBeTruthy();
  });

  test('whitespace-only text sends no request and shows nothing', async () => {
    await renderAdd();
    await typeName('   ');
    await wait(SEARCH_DEBOUNCE_MS * 2);
    expect(search.queries).toEqual([]);
    expect(screen.queryByTestId('add-create')).toBeNull();
    expect(screen.queryByTestId(/^add-suggestion-/)).toBeNull();
  });

  test('the name field accepts at most 100 characters and searches the trimmed text', async () => {
    await renderAdd();
    expect(screen.getByTestId('add-item-name').props.maxLength).toBe(100);
    await typeName('  milk  ');
    await wait(SEARCH_DEBOUNCE_MS);
    await screen.findByTestId(`add-suggestion-${MILK.id}`);
    expect(search.queries).toEqual(['milk']);
    expect(appQueryClient().getQueryData(queryKeys.households.catalogSearch(HOME.id, 'milk'))).toBeTruthy();
  });

  test('a suggestion shows the match in bold, its category and location, and its badges', async () => {
    search = catalogSearchHandler(() => [MILK, GOCHUJANG]);
    server.use(search.handler, pantryHandler(testPantry([testGroup('Milk', [testEntry(), testEntry()], { catalogItemId: MILK.id })])));
    await renderRouter(APP_DIR, { initialUrl: '/' });
    await screen.findByText('Milk');
    await press(screen.getByText('Add item'));
    await screen.findByTestId('add-item-name');

    await typeName('mil');
    await wait(SEARCH_DEBOUNCE_MS);
    const milk = await screen.findByTestId(`add-suggestion-${MILK.id}`);
    expect(within(milk).getByText('Dairy · Fridge')).toBeTruthy();
    expect(within(milk).getByText('2 in pantry')).toBeTruthy();
    expect(within(milk).queryByText('Household item')).toBeNull();
    expect(within(milk).getByTestId('add-suggestion-match')).toHaveTextContent('Mil');
    expect(milk.props.accessibilityRole).toBe('button');
    expect(milk.props.accessibilityLabel).toBe('Milk, Dairy, Fridge, 2 in pantry');

    const gochujang = screen.getByTestId(`add-suggestion-${GOCHUJANG.id}`);
    expect(within(gochujang).getByText('Produce · Fridge')).toBeTruthy();
    expect(within(gochujang).getByText('Household item')).toBeTruthy();
    expect(within(gochujang).queryByText(/in pantry/)).toBeNull();
  });

  test('with no cached pantry there is no pantry badge', async () => {
    search = catalogSearchHandler(() => [MILK]);
    server.use(search.handler);
    await renderAdd();
    await typeName('milk');
    await wait(SEARCH_DEBOUNCE_MS);
    const milk = await screen.findByTestId(`add-suggestion-${MILK.id}`);
    expect(within(milk).queryByText(/in pantry/)).toBeNull();
  });

  test('a failed search says so and still offers Create', async () => {
    search = catalogSearchHandler(() => apiError(500, 'internal'));
    server.use(search.handler);
    await renderAdd();
    await typeName('Gochujang');
    await wait(SEARCH_DEBOUNCE_MS);
    expect(await screen.findByText("Couldn't load suggestions")).toBeTruthy();
    expect(screen.getByTestId('add-create')).toBeTruthy();
    // Retried like any query: 1 + 2 retries.
    expect(search.queries).toEqual(['Gochujang', 'Gochujang', 'Gochujang']);
  });

  test('while a search runs, a loading indicator shows and the previous suggestions stay', async () => {
    const slow = deferred<typeof MILK[]>();
    search = catalogSearchHandler((query) => (query === 'milk' ? slow.promise : [MILK, OAT_MILK]));
    server.use(search.handler);
    await renderAdd();
    await typeName('mil');
    await wait(SEARCH_DEBOUNCE_MS);
    await screen.findByTestId(`add-suggestion-${OAT_MILK.id}`);

    await typeName('milk');
    await wait(SEARCH_DEBOUNCE_MS);
    expect(await screen.findByTestId('add-search-loading')).toBeTruthy();
    expect(screen.getByTestId(`add-suggestion-${OAT_MILK.id}`)).toBeTruthy();
    expect(screen.queryByTestId('add-create')).toBeNull();

    await act(async () => {
      slow.resolve([MILK]);
    });
    await wait(10);
    expect(screen.queryByTestId('add-search-loading')).toBeNull();
    expect(screen.queryByTestId(`add-suggestion-${OAT_MILK.id}`)).toBeNull();
  });
});

describe('Create option', () => {
  test('is offered only when no suggestion has the name, ignoring case and extra spaces', async () => {
    await renderAdd();

    await searchFor('milk');
    expect(screen.getByTestId(`add-suggestion-${OAT_MILK.id}`)).toBeTruthy();
    expect(screen.queryByTestId('add-create')).toBeNull();

    await searchFor('  MILK ');
    expect(screen.getByTestId(`add-suggestion-${MILK.id}`)).toBeTruthy();
    expect(screen.queryByTestId('add-create')).toBeNull();

    await searchFor('oat   MILK');
    expect(screen.getByTestId(`add-suggestion-${OAT_MILK.id}`)).toBeTruthy();
    expect(screen.queryByTestId(`add-suggestion-${MILK.id}`)).toBeNull();
    expect(screen.queryByTestId('add-create')).toBeNull();

    await searchFor('mil');
    expect(search.queries).toEqual(['milk', 'MILK', 'oat   MILK', 'mil']);
    const create = await screen.findByTestId('add-create');
    expect(within(create).getByText('Create “mil”')).toBeTruthy();
    expect(within(create).getByText('New item, just for Home')).toBeTruthy();
  });

  test('with no matches only Create is shown, with the cleaned name', async () => {
    await renderAdd();
    await typeName('  Gochujang    paste ');
    await wait(SEARCH_DEBOUNCE_MS);
    const create = await screen.findByTestId('add-create');
    expect(within(create).getByText('Create “Gochujang paste”')).toBeTruthy();
    expect(screen.queryByTestId(/^add-suggestion-/)).toBeNull();
  });

  test('a name over 60 characters cannot be created', async () => {
    await renderAdd();
    await typeName('x'.repeat(61));
    await wait(SEARCH_DEBOUNCE_MS);
    const create = await screen.findByTestId('add-create');
    expect(within(create).getByText('Names can be at most 60 characters')).toBeTruthy();
    expect(create.props.accessibilityState?.disabled).toBe(true);
    await press(create);
    expect(screen.queryByTestId('add-submit')).toBeNull();
  });
});

describe('details for a catalog item', () => {
  test('a built-in item with a shelf life is prefilled and adds with its defaults', async () => {
    const today = todayOnDevice();
    await renderAdd();
    await pick('milk', MILK);

    const summary = screen.getByTestId('add-summary');
    expect(within(summary).getByText('Milk')).toBeTruthy();
    expect(within(summary).getByText('Dairy')).toBeTruthy();
    expect(screen.queryByText('Edit')).toBeNull();
    expect(checked('level-picker-full')).toBe(true);
    expect(checked('add-location-1')).toBe(true);
    expect(checked('add-location-2')).toBe(false);
    expect(screen.getByText("Suggested from Milk's typical shelf life (7 days)")).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Add Milk' })).toBeTruthy();

    await submitAndWaitForReset();
    expect(lastEntry()).toMatchObject({
      catalogItemId: MILK.id,
      level: 'full',
      locationId: 1,
      purchasedOn: today,
      expiresOn: addDays(today, 7),
    });
    expect(screen.getByText('Added Milk to Fridge')).toBeTruthy();
  });

  test('an item with no shelf life has no expiry and no hint', async () => {
    await renderAdd();
    await pick('flour', FLOUR);
    expect(checked('add-location-3')).toBe(true);
    expect(screen.queryByText(/Suggested from/)).toBeNull();
    expect(screen.queryByTestId('add-expires-clear')).toBeNull();

    await submitAndWaitForReset();
    expect(lastEntry()).toMatchObject({ catalogItemId: FLOUR.id, locationId: 3, expiresOn: null });
    expect('expiresOn' in lastEntry()).toBe(true);
  });

  test("a household's location override is selected", async () => {
    await renderAdd();
    await pick('ice', ICE_CREAM);
    expect(checked('add-location-2')).toBe(true);
    expect(checked('add-location-1')).toBe(false);
  });

  test('the summary says how many are already in the pantry', async () => {
    server.use(pantryHandler(testPantry([testGroup('Milk', [testEntry(), testEntry()], { catalogItemId: MILK.id })])));
    await renderRouter(APP_DIR, { initialUrl: '/' });
    await screen.findByText('Milk');
    await press(screen.getByText('Add item'));
    await screen.findByTestId('add-item-name');
    await pick('milk', MILK);
    expect(within(screen.getByTestId('add-summary')).getByText('Dairy · already 2 in the pantry')).toBeTruthy();
  });

  test('the chosen level, location and dates are sent', async () => {
    await renderAdd();
    await pick('milk', MILK);
    await press(screen.getByTestId('level-picker-low'));
    await press(screen.getByTestId('add-location-2'));
    expect(checked('level-picker-low')).toBe(true);
    expect(checked('level-picker-full')).toBe(false);
    await chooseDate('add-bought', '2026-10-30');
    await submitAndWaitForReset();
    expect(lastEntry()).toMatchObject({ level: 'low', locationId: 2, purchasedOn: '2026-10-30', expiresOn: '2026-11-06' });
    expect(screen.getByText('Added Milk to Freezer')).toBeTruthy();
  });

  test('changing Bought moves a suggested expiry', async () => {
    await renderAdd();
    await pick('milk', MILK);
    await chooseDate('add-bought', '2028-02-25');
    expect(screen.getByText("Suggested from Milk's typical shelf life (7 days)")).toBeTruthy();
    await submitAndWaitForReset();
    expect(lastEntry()).toMatchObject({ purchasedOn: '2028-02-25', expiresOn: '2028-03-03' });
  });

  test('an expiry set by hand stays when Bought changes', async () => {
    await renderAdd();
    await pick('milk', MILK);
    await chooseDate('add-expires', '2026-12-24');
    expect(screen.queryByText(/Suggested from/)).toBeNull();
    await chooseDate('add-bought', '2026-10-01');
    await submitAndWaitForReset();
    expect(lastEntry()).toMatchObject({ purchasedOn: '2026-10-01', expiresOn: '2026-12-24' });
  });

  test('an expiry cleared by hand stays cleared when Bought changes, and is sent as null', async () => {
    await renderAdd();
    await pick('milk', MILK);
    await press(screen.getByLabelText('Clear expiry date'));
    expect(screen.queryByText(/Suggested from/)).toBeNull();
    await chooseDate('add-bought', '2026-10-01');
    await submitAndWaitForReset();
    expect(lastEntry()).toMatchObject({ purchasedOn: '2026-10-01', expiresOn: null });
  });

  test('"Change" goes back to the search with the typed text', async () => {
    await renderAdd();
    await pick('milk', MILK);
    await press(screen.getByTestId('add-change'));
    expect(screen.getByTestId('add-item-name').props.value).toBe('milk');
    expect(await screen.findByTestId(`add-suggestion-${MILK.id}`)).toBeTruthy();
  });
});

describe('details for a new item', () => {
  test('creates the item, then the entry, and refreshes catalog search', async () => {
    const today = todayOnDevice();
    const items = createCatalogItemHandler();
    server.use(items.handler);
    await renderAdd();
    await pickCreate('  gochujang   paste ');

    const summary = screen.getByTestId('add-summary');
    expect(within(summary).getByText('gochujang paste')).toBeTruthy();
    expect(within(summary).getByText('New item, just for Home')).toBeTruthy();
    expect(screen.getByText('Choose a category')).toBeTruthy();
    expect(screen.getByText('Choose a location')).toBeTruthy();
    expect(checked('add-location-1')).toBe(false);
    expect(screen.getByTestId('add-submit').props.accessibilityState?.disabled).toBe(true);

    await press(screen.getByTestId('add-category-3'));
    expect(screen.getByTestId('add-submit').props.accessibilityState?.disabled).toBe(true);
    await press(screen.getByTestId('add-location-1'));
    expect(screen.queryByText('Choose a category')).toBeNull();
    expect(screen.queryByText('Choose a location')).toBeNull();
    expect(screen.getByTestId('add-submit').props.accessibilityState?.disabled).toBe(false);

    await act(async () => {
      fireEvent.changeText(screen.getByTestId('add-shelf-life'), '365');
    });
    expect(screen.getByText("Suggested from gochujang paste's typical shelf life (365 days)")).toBeTruthy();

    await submitAndWaitForReset();
    expect(items.requests).toEqual([
      { householdId: HOME.id, body: { name: 'gochujang paste', categoryId: 3, locationId: 1, shelfLifeDays: 365 } },
    ]);
    expect(lastEntry()).toMatchObject({
      catalogItemId: items.created[0].id,
      locationId: 1,
      purchasedOn: today,
      expiresOn: addDays(today, 365),
    });
    expect(screen.getByText('Added gochujang paste to Fridge')).toBeTruthy();
    const state = appQueryClient().getQueryState(queryKeys.households.catalogSearch(HOME.id, 'gochujang   paste'));
    expect(state?.isInvalidated).toBe(true);
  });

  test('no shelf life is left out of the request, and a bad one blocks saving', async () => {
    const items = createCatalogItemHandler();
    server.use(items.handler);
    await renderAdd();
    await pickCreate('Gochujang');
    await press(screen.getByTestId('add-category-3'));
    await press(screen.getByTestId('add-location-1'));

    for (const bad of ['0', '3651', '1.5', 'abc']) {
      await act(async () => {
        fireEvent.changeText(screen.getByTestId('add-shelf-life'), bad);
      });
      expect(screen.getByText('Enter a whole number of days from 1 to 3650')).toBeTruthy();
      expect(screen.getByTestId('add-submit').props.accessibilityState?.disabled).toBe(true);
    }
    await act(async () => {
      fireEvent.changeText(screen.getByTestId('add-shelf-life'), '');
    });
    await submitAndWaitForReset();
    expect(items.requests[0].body).toEqual({ name: 'Gochujang', categoryId: 3, locationId: 1 });
    expect(lastEntry().expiresOn).toBeNull();
  });

  test('a 409 name_taken uses the existing item and creates nothing until Add is tapped again', async () => {
    let created = false;
    search = catalogSearchHandler((query) => (created && query.toLowerCase() === 'gochujang' ? [GOCHUJANG] : []));
    const items = createCatalogItemHandler(() => {
      created = true;
      return apiError(409, 'name_taken');
    });
    server.use(search.handler, items.handler);
    await renderAdd();
    await pickCreate('gochujang');
    await press(screen.getByTestId('add-category-2'));
    await press(screen.getByTestId('add-location-3'));
    await press(screen.getByTestId('level-picker-half'));
    await chooseDate('add-bought', '2026-10-15');
    await submit();

    expect(await screen.findByText('Gochujang already exists, so we used it')).toBeTruthy();
    expect(entries.requests).toHaveLength(0);
    expect(screen.queryByTestId('add-category')).toBeNull();
    expect(screen.queryByTestId('add-shelf-life')).toBeNull();
    expect(checked('add-location-3')).toBe(true);
    expect(checked('level-picker-half')).toBe(true);
    expect(screen.getByRole('button', { name: 'Add Gochujang' })).toBeTruthy();

    await submitAndWaitForReset();
    expect(items.requests).toHaveLength(1);
    expect(entries.requests).toHaveLength(1);
    expect(lastEntry()).toMatchObject({ catalogItemId: GOCHUJANG.id, level: 'half', locationId: 3, purchasedOn: '2026-10-15', expiresOn: null });
  });

  test('a 409 name_taken with no matching result keeps the form', async () => {
    search = catalogSearchHandler(() => []);
    const items = createCatalogItemHandler(() => apiError(409, 'name_taken'));
    server.use(search.handler, items.handler);
    await renderAdd();
    await pickCreate('Gochujang');
    await press(screen.getByTestId('add-category-3'));
    await press(screen.getByTestId('add-location-1'));
    await submit();

    expect(await screen.findByText('An item named Gochujang already exists')).toBeTruthy();
    expect(screen.getByTestId('add-category')).toBeTruthy();
    expect(checked('add-category-3')).toBe(true);
    expect(entries.requests).toHaveLength(0);
  });

  test('when the entry fails after the item was created, a retry creates no second item', async () => {
    const items = createCatalogItemHandler();
    entries = createEntryHandler((_body, call) => (call <= 3 ? apiError(500, 'internal') : undefined));
    server.use(items.handler, entries.handler);
    await renderAdd();
    await pickCreate('Gochujang');
    await press(screen.getByTestId('add-category-3'));
    await press(screen.getByTestId('add-location-1'));
    await submit();

    expect(await screen.findByText('The server ran into a problem. Please try again in a moment.')).toBeTruthy();
    expect(items.requests).toHaveLength(1);
    expect(entries.requests).toHaveLength(3);
    // The form now adds the created item, as if it had been picked from the search.
    expect(screen.queryByTestId('add-category')).toBeNull();

    await submitAndWaitForReset();
    expect(items.requests).toHaveLength(1);
    expect(entries.requests).toHaveLength(4);
    const ids = entries.requests.map((request) => `${request.body.id}/${request.body.eventId}`);
    expect(new Set(ids).size).toBe(1);
    expect(lastEntry().catalogItemId).toBe(items.created[0].id);
  });

  test("a failing categories request says so with Retry, and Add stays disabled", async () => {
    let fail = true;
    server.use(
      http.get(apiUrl('/categories'), () =>
        fail ? apiError(500, 'internal') : HttpResponse.json({ items: [{ id: 3, key: 'baking', name: 'Baking' }] }),
      ),
    );
    await renderAdd();
    await pickCreate('Gochujang');
    expect(await screen.findByText("Couldn't load categories")).toBeTruthy();
    expect(screen.getByTestId('add-submit').props.accessibilityState?.disabled).toBe(true);

    fail = false;
    await press(screen.getByTestId('add-categories-retry'));
    expect(await screen.findByTestId('add-category-3')).toBeTruthy();
    expect(screen.queryByText("Couldn't load categories")).toBeNull();
  });
});

describe('saving', () => {
  test('a retried save sends the same UUIDv7 id and eventId', async () => {
    entries = createEntryHandler((_body, call) => (call === 1 ? apiError(400, 'invalid_level') : undefined));
    server.use(entries.handler);
    await renderAdd();
    await pick('milk', MILK);
    await submit();
    expect(await screen.findByText("That didn't work. Check the details and try again.")).toBeTruthy();
    expect(entries.requests).toHaveLength(1);
    expect(checked('add-location-1')).toBe(true);

    await submitAndWaitForReset();
    const [first, second] = entries.requests.map((request) => request.body);
    expect(first.id).toMatch(UUID_V7);
    expect(first.eventId).toMatch(UUID_V7);
    expect(first.id).not.toBe(first.eventId);
    expect(second.id).toBe(first.id);
    expect(second.eventId).toBe(first.eventId);
  });

  test('a network error or 5xx is retried automatically with the same IDs, and a 200 counts as success', async () => {
    entries = createEntryHandler((body, call) => {
      if (call === 1) return HttpResponse.error();
      if (call === 2) return apiError(503, 'unavailable');
      return HttpResponse.json({ ...body, locationId: 1, level: 'full', purchasedOn: '2026-11-02', expiresOn: null, createdAt: '2026-11-02T15:00:00Z', updatedAt: '2026-11-02T15:00:00Z' }, { status: 200 });
    });
    server.use(entries.handler);
    await renderAdd();
    await pick('milk', MILK);
    await submitAndWaitForReset();
    expect(entries.requests).toHaveLength(3);
    expect(new Set(entries.requests.map((request) => request.body.id)).size).toBe(1);
    expect(new Set(entries.requests.map((request) => request.body.eventId)).size).toBe(1);
    expect(screen.getByText('Added Milk to Fridge')).toBeTruthy();
  });

  test('picking another item makes new IDs', async () => {
    entries = createEntryHandler((_body, call) => (call === 1 ? apiError(400, 'invalid_level') : undefined));
    server.use(entries.handler);
    await renderAdd();
    await pick('milk', MILK);
    await submit();
    await screen.findByTestId('add-error');
    await press(screen.getByTestId('add-change'));
    await press(await screen.findByTestId(`add-suggestion-${MILK.id}`));
    await submitAndWaitForReset();

    const [first, second] = entries.requests.map((request) => request.body);
    expect(second.id).toMatch(UUID_V7);
    expect(second.id).not.toBe(first.id);
    expect(second.eventId).not.toBe(first.eventId);
  });

  test('after a 409 entry_id_conflict the error shows and the next attempt uses new IDs', async () => {
    entries = createEntryHandler((_body, call) => (call === 1 ? apiError(409, 'entry_id_conflict') : undefined));
    server.use(entries.handler);
    await renderAdd();
    await pick('milk', MILK);
    await submit();
    expect(await screen.findByText('This was changed by someone else. Refresh and try again.')).toBeTruthy();
    await submitAndWaitForReset();
    const [first, second] = entries.requests.map((request) => request.body);
    expect(second.id).not.toBe(first.id);
    expect(second.eventId).not.toBe(first.eventId);
  });

  test('while saving, Add is busy, so a double tap creates one entry', async () => {
    const hold = deferred<void>();
    entries = createEntryHandler(async () => {
      await hold.promise;
      return undefined;
    });
    server.use(entries.handler);
    await renderAdd();
    await pick('milk', MILK);
    await submit();
    expect(screen.getByTestId('add-submit').props.accessibilityState).toMatchObject({ busy: true, disabled: true });
    await submit();
    await act(async () => {
      hold.resolve();
    });
    await screen.findByTestId('add-confirmation');
    expect(entries.requests).toHaveLength(1);
  });

  test('after a save the form is empty on the Search step, focused, and Pantry shows the new entry', async () => {
    let groups = [testGroup('Milk', [testEntry()], { catalogItemId: MILK.id })];
    let pantryLoads = 0;
    server.use(
      http.get(apiUrl('/households/:householdId/pantry'), () => {
        pantryLoads += 1;
        return HttpResponse.json(testPantry(groups));
      }),
    );
    await renderRouter(APP_DIR, { initialUrl: '/' });
    await screen.findByText('Milk');
    await press(screen.getByText('Add item'));
    await screen.findByTestId('add-item-name');
    await pick('milk', MILK);
    const loadsBefore = pantryLoads;
    groups = [testGroup('Milk', [testEntry(), testEntry()], { catalogItemId: MILK.id })];
    await submitAndWaitForReset();

    const name = screen.getByTestId('add-item-name');
    expect(name.props.value).toBe('');
    expect(name.props.autoFocus).toBe(true);
    expect(screen.queryByTestId('add-submit')).toBeNull();
    expect(screen.getByText('Added Milk to Fridge')).toBeTruthy();
    await act(async () => {});
    expect(pantryLoads).toBeGreaterThan(loadsBefore);

    await press(screen.getByLabelText('Back to Pantry'));
    expect(await screen.findByText('×2')).toBeTruthy();
  });

  test('API errors keep the form values', async () => {
    entries = createEntryHandler(() => HttpResponse.error());
    server.use(entries.handler);
    await renderAdd();
    await pick('milk', MILK);
    await press(screen.getByTestId('add-location-3'));
    await submit();
    expect(await screen.findByText("Can't reach the server. Check your connection and try again.")).toBeTruthy();
    expect(entries.requests).toHaveLength(3);
    expect(checked('add-location-3')).toBe(true);
  });
});

describe('keyboard (web)', () => {
  beforeEach(() => {
    mockUsesKeyboardHighlight.mockReturnValue(true);
    mockFocusElement.mockClear();
  });
  afterEach(() => {
    mockUsesKeyboardHighlight.mockReturnValue(false);
  });

  async function key(name: string) {
    await act(async () => {
      fireEvent(screen.getByTestId('add-item-name'), 'keyPress', { nativeEvent: { key: name }, preventDefault: jest.fn() });
    });
  }

  async function enter() {
    await act(async () => {
      fireEvent(screen.getByTestId('add-item-name'), 'submitEditing', { nativeEvent: { text: '' } });
    });
  }

  test('"milk", Enter and Enter again adds Milk with its defaults', async () => {
    const today = todayOnDevice();
    await renderAdd();
    await searchFor('milk');
    await enter();
    expect(within(screen.getByTestId('add-summary')).getByText('Milk')).toBeTruthy();
    expect(lastFocused()).toBe('add-submit');
    // Focus is now on Add; a second Enter presses it.
    await submitAndWaitForReset();
    expect(lastEntry()).toMatchObject({ catalogItemId: MILK.id, level: 'full', locationId: 1, purchasedOn: today, expiresOn: addDays(today, 7) });
  });

  test('Up and Down move the highlight through the suggestions and Create', async () => {
    await renderAdd();
    await searchFor('mil');
    // Milk, Oat milk, then Create "mil".
    await key('ArrowDown');
    await key('ArrowDown');
    await enter();
    expect(within(screen.getByTestId('add-summary')).getByText('New item, just for Home')).toBeTruthy();

    await press(screen.getByTestId('add-change'));
    await screen.findByTestId(`add-suggestion-${OAT_MILK.id}`);
    await key('ArrowUp');
    await key('ArrowUp');
    await enter();
    expect(within(screen.getByTestId('add-summary')).getByText('Oat milk')).toBeTruthy();
  });

  test('hovering a suggestion does not move the highlight, so Enter still picks the first', async () => {
    await renderAdd();
    await searchFor('milk');
    // Browsers report a hover when results appear under a pointer that has not moved.
    await act(async () => {
      fireEvent(screen.getByTestId(`add-suggestion-${OAT_MILK.id}`), 'hoverIn', { nativeEvent: {} });
    });
    await enter();
    expect(within(screen.getByTestId('add-summary')).getByText('Milk')).toBeTruthy();
  });

  test('new results move the highlight back to the first option', async () => {
    await renderAdd();
    await searchFor('mil');
    await key('ArrowDown');
    // "milk" finds the same two items: still new results, so Milk is highlighted again.
    await searchFor('milk');
    expect(screen.getByTestId(`add-suggestion-${OAT_MILK.id}`)).toBeTruthy();
    await enter();
    expect(within(screen.getByTestId('add-summary')).getByText('Milk')).toBeTruthy();
  });

  test('Create picked with Enter moves focus to the category, as Add is not usable yet', async () => {
    await renderAdd();
    await searchFor('Gochujang');
    await enter();
    expect(within(screen.getByTestId('add-summary')).getByText('New item, just for Home')).toBeTruthy();
    expect(lastFocused()).toBe('add-category-1');
  });

  test('when locations fail to load, focus goes to their Retry', async () => {
    server.use(http.get(apiUrl('/locations'), () => apiError(500, 'internal')));
    await renderAdd();
    await searchFor('milk');
    await enter();
    await screen.findByText("Couldn't load locations");
    expect(lastFocused()).toBe('add-locations-retry');
  });

  test('on phones, Enter picks nothing', async () => {
    mockUsesKeyboardHighlight.mockReturnValue(false);
    await renderAdd();
    await searchFor('milk');
    await enter();
    expect(screen.queryByTestId('add-summary')).toBeNull();
  });
});

describe('visits', () => {
  test('leaving /add and coming back starts empty', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/' });
    await press(await screen.findByText('Add item'));
    await screen.findByTestId('add-item-name');
    await pick('milk', MILK);

    await press(screen.getByLabelText('Back to Pantry'));
    await press(await screen.findByText('Add item'));
    const name = await screen.findByTestId('add-item-name');
    expect(name.props.value).toBe('');
    expect(name.props.autoFocus).toBe(true);
    expect(screen.queryByTestId('add-submit')).toBeNull();
    expect(screen.queryByTestId(/^add-suggestion-/)).toBeNull();
  });

  test('a failing locations request says so with Retry, and Add stays disabled', async () => {
    server.use(http.get(apiUrl('/locations'), () => apiError(500, 'internal')));
    await renderAdd();
    await pick('milk', MILK);
    expect(await screen.findByText("Couldn't load locations")).toBeTruthy();
    expect(screen.getByTestId('add-locations-retry')).toBeTruthy();
    expect(screen.getByTestId('add-submit').props.accessibilityState?.disabled).toBe(true);
  });

  test('switching household while saving goes to Pantry and confirms nothing there', async () => {
    await setWindowWidth(1024);
    const hold = deferred<void>();
    entries = createEntryHandler(async () => {
      await hold.promise;
      return undefined;
    });
    server.use(...signedInApiHandlers(ME, [HOME, CABIN]), search.handler, entries.handler);
    // Cabin (first by name) is current. Open Pantry first, so its pantry is cached.
    await renderRouter(APP_DIR, { initialUrl: '/' });
    await screen.findByText('Your pantry is empty');
    await press(screen.getByText('Add item'));
    await screen.findByTestId('add-item-name');
    await pick('milk', MILK);
    await submit();

    await press(screen.getByLabelText('Switch household, current: Cabin'));
    await press(screen.getByLabelText(householdRowLabel(HOME, false)));
    await screen.findByLabelText('Switch household, current: Home');
    expect(screen).toHavePathname('/');

    await act(async () => {
      hold.resolve();
    });
    await waitFor(() =>
      expect(appQueryClient().getQueryState(queryKeys.households.pantry(CABIN.id))?.isInvalidated).toBe(true),
    );
    expect(entries.requests).toHaveLength(1);
    expect(entries.requests[0].householdId).toBe(CABIN.id);
    expect(screen.queryByText(/^Added /)).toBeNull();
    expect(screen).toHavePathname('/');
  });
});

import type { QueryClient } from '@tanstack/react-query';
import { act, fireEvent, waitFor, within } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { http, HttpResponse } from 'msw';
import { Dimensions, StyleSheet } from 'react-native';

import { queryKeys } from '@/api/query-keys';
import { householdRowLabel } from '@/components/household-switcher';
import { colors, CONTENT_MAX_WIDTH } from '@/theme';
import { apiUrl, setupApiServer } from '../test-utils/api';
import { signedInApiHandlers, signInTestUser, testHousehold, testMe } from '../test-utils/auth';
import { pantryViewKey } from '@/pantry/view-storage';
import {
  categoriesHandler,
  locationsHandler,
  pantryHandler,
  TEST_LOCATIONS,
  testEntry,
  testGroup,
  testPantry,
} from '../test-utils/pantry';

jest.setTimeout(120000);

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

// Retry without delay, and keep the app's QueryClient so a test can refetch the pantry.
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
const SAM_ID = '0190f3a1-7c2e-7b3d-9a4f-00000000a001';
const HOME = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000a', name: 'Home', role: 'owner' });
const CABIN = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000b', name: 'Cabin', role: 'member' });

const server = setupApiServer(...signedInApiHandlers(ME, [HOME]));

let uid = '';

beforeEach(async () => {
  uid = signInTestUser().uid;
  await setWindowWidth(390);
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

async function renderPantry() {
  await renderRouter(APP_DIR, { initialUrl: '/' });
}

/** A row's test ID: rows are keyed by section, because one item can appear in several. By default, the Fridge section. */
function rowTestId(group: { catalogItemId: string }, sectionKey = 'location-1') {
  return `pantry-row-${sectionKey}:${group.catalogItemId}`;
}

function textStyle(text: string) {
  return StyleSheet.flatten(screen.getByText(text).props.style);
}

const MILK_OLD = testEntry({ level: 'low', purchasedOn: '2026-10-20', expiresOn: '2026-11-03', expiryStatus: 'soon' });
const MILK_NEW = testEntry({ level: 'full', purchasedOn: '2026-10-27', expiresOn: '2026-11-10' });
const MILK = testGroup('Milk', [MILK_OLD, MILK_NEW]);
const BUTTER_ENTRY = testEntry({
  level: 'half',
  locationId: 1,
  purchasedOn: '2025-12-12',
  expiresOn: null,
  lastChange: {
    type: 'level_changed',
    changes: { level: { from: 'full', to: 'half' } },
    actorId: SAM_ID,
    actorName: 'Sam',
    occurredAt: new Date(Date.now() - 2 * 60 * 60 * 1000).toISOString(),
  },
});
const BUTTER = testGroup('Butter', [BUTTER_ENTRY]);

describe('rows', () => {
  test('one row per group in the API order; a count for two entries and none for one', async () => {
    server.use(pantryHandler(testPantry([BUTTER, MILK])));
    await renderPantry();

    const butterRow = await screen.findByTestId(rowTestId(BUTTER));
    const milkRow = screen.getByTestId(rowTestId(MILK));
    expect(within(milkRow).getByText('Milk')).toBeTruthy();
    expect(within(milkRow).getByTestId('pantry-row-count')).toHaveTextContent('×2');
    expect(within(butterRow).getByText('Butter')).toBeTruthy();
    expect(within(butterRow).queryByTestId('pantry-row-count')).toBeNull();
    expect(within(butterRow).queryByText(/×1/)).toBeNull();

    const names = screen.getAllByTestId(/^pantry-row-location-1:/).map((row) => row.props.testID);
    expect(names).toEqual([rowTestId(BUTTER), rowTestId(MILK)]);
  });

  test('the list is virtualised: 500 entries over 200 items render only the rows near the screen', async () => {
    const groups = Array.from({ length: 200 }, (_, index) => {
      const name = `Item ${String(index).padStart(3, '0')}`;
      const count = index < 100 ? 3 : 2;
      return testGroup(name, Array.from({ length: count }, () => testEntry()));
    });
    expect(groups.reduce((sum, group) => sum + group.entries.length, 0)).toBe(500);
    server.use(pantryHandler(testPantry(groups)));
    await renderPantry();
    await screen.findByText('Item 000');

    const rendered = screen.getAllByTestId(/^pantry-row-location-1:/).length;
    expect(rendered).toBeGreaterThan(0);
    expect(rendered).toBeLessThan(60);
    expect(screen.queryByText('Item 199')).toBeNull();
  });

  test('a single entry shows its details directly and opens /entries/<id>', async () => {
    server.use(pantryHandler(testPantry([BUTTER])));
    await renderPantry();

    const row = await screen.findByTestId(rowTestId(BUTTER));
    expect(within(row).getByText('Bought Dec 12, 2025 · Fridge')).toBeTruthy();
    expect(within(row).getByText('Half')).toBeTruthy();
    expect(within(row).getByText('No expiry')).toBeTruthy();
    expect(within(row).getByText('Set to Half by Sam, 2h ago')).toBeTruthy();

    await press(within(row).getByText('Butter'));
    expect(screen).toHavePathname(`/entries/${BUTTER_ENTRY.id}`);
  });

  test('tapping a row with two entries expands it, and tapping again collapses it', async () => {
    server.use(pantryHandler(testPantry([MILK])));
    await renderPantry();

    const header = await screen.findByRole('button', { name: /^Milk, 2 entries/ });
    // Collapsed: a summary of the levels and the most urgent expiry, no entries.
    expect(screen.getByText('Low, Full')).toBeTruthy();
    expect(screen.getByText('Expires tomorrow')).toBeTruthy();
    expect(screen.queryByTestId(`pantry-entry-${MILK_OLD.id}`)).toBeNull();
    expect(header.props.accessibilityState).toMatchObject({ expanded: false });

    await press(header);
    const old = screen.getByTestId(`pantry-entry-${MILK_OLD.id}`);
    const fresh = screen.getByTestId(`pantry-entry-${MILK_NEW.id}`);
    expect(within(old).getByText('Bought Oct 20 · Fridge')).toBeTruthy();
    expect(within(old).getByText('Low')).toBeTruthy();
    expect(within(old).getByText('Expires tomorrow')).toBeTruthy();
    expect(within(fresh).getByText('Bought Oct 27 · Fridge')).toBeTruthy();
    expect(within(fresh).getByText('Expires Nov 10')).toBeTruthy();
    expect(screen.queryByText('Low, Full')).toBeNull();
    expect(screen.getByRole('button', { name: /^Milk, 2 entries/ }).props.accessibilityState).toMatchObject({
      expanded: true,
    });

    await press(screen.getByRole('button', { name: /^Milk, 2 entries/ }));
    expect(screen.queryByTestId(`pantry-entry-${MILK_OLD.id}`)).toBeNull();
    expect(screen.getByText('Low, Full')).toBeTruthy();
  });

  test('tapping an entry of an expanded row opens it', async () => {
    server.use(pantryHandler(testPantry([MILK])));
    await renderPantry();

    await press(await screen.findByRole('button', { name: /^Milk, 2 entries/ }));
    await press(screen.getByTestId(`pantry-entry-${MILK_NEW.id}`));
    expect(screen).toHavePathname(`/entries/${MILK_NEW.id}`);
  });

  test('a screen reader reads a collapsed row as one item', async () => {
    server.use(pantryHandler(testPantry([MILK])));
    await renderPantry();

    expect(await screen.findByRole('button', { name: 'Milk, 2 entries, Low, Full, one expires tomorrow' })).toBeTruthy();
  });

  test('expanded rows stay expanded when the data is refetched', async () => {
    server.use(pantryHandler(testPantry([MILK])));
    await renderPantry();
    await press(await screen.findByRole('button', { name: /^Milk, 2 entries/ }));
    expect(screen.getByTestId(`pantry-entry-${MILK_OLD.id}`)).toBeTruthy();

    const third = testEntry({ level: 'half', purchasedOn: '2026-11-01' });
    server.use(pantryHandler(testPantry([testGroup('Milk', [MILK_OLD, MILK_NEW, third], { catalogItemId: MILK.catalogItemId })])));
    await act(async () => {
      await appQueryClient().refetchQueries({ queryKey: queryKeys.households.pantry(HOME.id) });
    });

    expect(await screen.findByTestId(`pantry-entry-${third.id}`)).toBeTruthy();
    expect(screen.getByTestId(`pantry-entry-${MILK_OLD.id}`)).toBeTruthy();
  });

  test('expanded rows reset when the household changes', async () => {
    server.use(...signedInApiHandlers(ME, [HOME, CABIN]));
    server.use(pantryHandler(() => testPantry([MILK])));
    await renderPantry();
    await press(await screen.findByRole('button', { name: /^Milk, 2 entries/ }));
    expect(screen.getByTestId(`pantry-entry-${MILK_OLD.id}`)).toBeTruthy();

    await press(screen.getByLabelText('Switch household, current: Cabin'));
    await press(screen.getByLabelText(householdRowLabel(HOME, false)));

    await screen.findByLabelText('Switch household, current: Home');
    expect(await screen.findByRole('button', { name: /^Milk, 2 entries/ })).toBeTruthy();
    expect(screen.queryByTestId(`pantry-entry-${MILK_OLD.id}`)).toBeNull();
  });

  test('a 60-character name wraps within the row and keeps its count', async () => {
    const name = 'Extra virgin cold pressed olive oil from the Peloponnese 1 L';
    expect(name).toHaveLength(60);
    server.use(pantryHandler(testPantry([testGroup(name, [testEntry(), testEntry()])])));
    await renderPantry();

    const nameText = await screen.findByText(name);
    expect(nameText.props.numberOfLines).toBe(2);
    expect(StyleSheet.flatten(nameText.props.style).flexShrink).toBe(1);
    expect(screen.getByTestId('pantry-row-count')).toHaveTextContent('×2');
    expect(StyleSheet.flatten(screen.getByTestId('pantry-row-count').parent?.props.style).flexShrink).toBe(0);
  });
});

describe('expiry and out', () => {
  test.each([
    ['2026-11-02', 'Expires today'],
    ['2026-11-03', 'Expires tomorrow'],
    ['2026-11-05', 'Expires in 3 days'],
  ])('soon (%s) uses the expiring-soon highlight and says "%s"', async (expiresOn, text) => {
    const group = testGroup('Yogurt', [testEntry({ expiresOn, expiryStatus: 'soon' })]);
    server.use(pantryHandler(testPantry([group])));
    await renderPantry();

    expect(await screen.findByText(text)).toBeTruthy();
    expect(textStyle(text).color).toBe(colors.light.expiringSoon);
    expect(screen.getByTestId('expiry-chip-soon')).toHaveStyle({ backgroundColor: colors.light.expiringSubtle });
    expect(screen.getByTestId(rowTestId(group))).toHaveStyle({ borderColor: colors.light.expiringSoon });
  });

  test('expired says "Expired Oct 25" in the danger colour', async () => {
    const group = testGroup('Yogurt', [testEntry({ expiresOn: '2026-10-25', expiryStatus: 'expired' })]);
    server.use(pantryHandler(testPantry([group])));
    await renderPantry();

    expect(await screen.findByText('Expired Oct 25')).toBeTruthy();
    expect(textStyle('Expired Oct 25').color).toBe(colors.light.danger);
    expect(screen.getByTestId('expiry-chip-expired')).toHaveStyle({ backgroundColor: colors.light.dangerSubtle });
    expect(screen.getByTestId(rowTestId(group))).toHaveStyle({ borderColor: colors.light.danger });
  });

  test('a later expiry and no expiry are plain', async () => {
    server.use(
      pantryHandler(
        testPantry([
          testGroup('Butter', [testEntry({ expiresOn: '2026-12-12' })]),
          testGroup('Flour', [testEntry({ expiresOn: null })]),
        ]),
      ),
    );
    await renderPantry();

    expect(await screen.findByText('Expires Dec 12')).toBeTruthy();
    expect(screen.getByText('No expiry')).toBeTruthy();
    expect(screen.queryByTestId(/^expiry-chip/)).toBeNull();
  });

  test('an out entry inside the window is muted and never highlighted', async () => {
    // The API returns `expiryStatus: null` for out entries.
    const group = testGroup('Spinach', [testEntry({ level: 'out', expiresOn: '2026-11-03', expiryStatus: null })]);
    server.use(pantryHandler(testPantry([group])));
    await renderPantry();

    expect(await screen.findByText('Expires Nov 3')).toBeTruthy();
    expect(screen.queryByTestId(/^expiry-chip/)).toBeNull();
    expect(textStyle('Expires Nov 3').color).toBe(colors.light.mutedText);
    expect(textStyle('Spinach').color).toBe(colors.light.mutedText);
    expect(textStyle('Out').color).toBe(colors.light.mutedText);
    expect(screen.getByTestId(rowTestId(group))).toHaveStyle({ borderColor: colors.light.border });
  });

  test('a collapsed row is highlighted when any entry is soon or expired, with words', async () => {
    const group = testGroup('Milk', [
      testEntry({ expiresOn: '2026-12-01' }),
      testEntry({ expiresOn: '2026-10-25', expiryStatus: 'expired' }),
    ]);
    server.use(pantryHandler(testPantry([group])));
    await renderPantry();

    expect(await screen.findByText('Expired Oct 25')).toBeTruthy();
    expect(screen.getByTestId(rowTestId(group))).toHaveStyle({ borderColor: colors.light.danger });
  });

  test('a row whose entries are all out is muted as a whole, and an out entry is never the summary\'s expiry', async () => {
    const group = testGroup('Rice', [
      testEntry({ level: 'out', expiresOn: '2026-11-03' }),
      testEntry({ level: 'out', expiresOn: null }),
    ]);
    server.use(pantryHandler(testPantry([group])));
    await renderPantry();

    expect(await screen.findByText('Rice')).toBeTruthy();
    expect(textStyle('Rice').color).toBe(colors.light.mutedText);
    expect(textStyle('Out, Out').color).toBe(colors.light.mutedText);
    expect(screen.queryByText('Expires Nov 3')).toBeNull();
    expect(screen.getByRole('button', { name: 'Rice, 2 entries, all out' })).toBeTruthy();
  });

  test('the expiry wording follows the API\'s today, not the device clock', async () => {
    const group = testGroup('Milk', [testEntry({ expiresOn: '2030-05-02', expiryStatus: 'soon' })]);
    server.use(pantryHandler(testPantry([group], '2030-05-01')));
    await renderPantry();

    expect(await screen.findByText('Expires tomorrow')).toBeTruthy();
  });
});

describe('last-change label', () => {
  test('"by you" for the signed-in user\'s own change, no name for a deleted account', async () => {
    const hour = 60 * 60 * 1000;
    server.use(
      pantryHandler(
        testPantry([
          testGroup('Eggs', [
            testEntry({
              lastChange: { type: 'entry_created', changes: {}, actorId: ME.id, actorName: ME.displayName, occurredAt: new Date(Date.now() - 3 * hour).toISOString() },
            }),
          ]),
          testGroup('Peas', [
            testEntry({
              lastChange: {
                type: 'location_changed',
                changes: { location_id: { from: 1, to: 2 } },
                actorId: null,
                actorName: null,
                occurredAt: new Date(Date.now() - 5 * 60 * 1000).toISOString(),
              },
            }),
          ]),
          testGroup('Salt', [testEntry({ lastChange: null })]),
        ]),
      ),
    );
    await renderPantry();

    expect(await screen.findByText('Added by you, 3h ago')).toBeTruthy();
    expect(screen.getByText('Moved to Freezer, 5m ago')).toBeTruthy();
    expect(screen.getAllByTestId('last-change')).toHaveLength(2);
  });

  test('labels update at least once a minute while the screen is open', async () => {
    server.use(
      pantryHandler(
        testPantry([
          testGroup('Eggs', [
            testEntry({
              lastChange: { type: 'entry_created', changes: {}, actorId: SAM_ID, actorName: 'Sam', occurredAt: new Date(Date.now() - 30 * 1000).toISOString() },
            }),
          ]),
        ]),
      ),
    );
    await renderPantry();
    expect(await screen.findByText('Added by Sam, just now')).toBeTruthy();

    await act(async () => {
      jest.advanceTimersByTime(60 * 1000);
    });
    expect(screen.getByText(/^Added by Sam, [12]m ago$/)).toBeTruthy();
  });
});

describe('screen states', () => {
  test('first load shows a loading placeholder, never the empty state', async () => {
    let release: () => void = () => {};
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    server.use(
      http.get(apiUrl('/households/:householdId/pantry'), async () => {
        await held;
        return HttpResponse.json(testPantry());
      }),
    );
    await renderPantry();

    expect(await screen.findByTestId('pantry-loading')).toBeTruthy();
    expect(screen.queryByText('Your pantry is empty')).toBeNull();
    expect(screen.getByTestId('add-item-button')).toBeTruthy();

    await act(async () => {
      release();
    });
    expect(await screen.findByText('Your pantry is empty')).toBeTruthy();
    expect(screen.queryByTestId('pantry-loading')).toBeNull();
  });

  test('an empty household shows "Your pantry is empty" with a button that opens Add item', async () => {
    await renderPantry();

    expect(await screen.findByText('Your pantry is empty')).toBeTruthy();
    await press(screen.getByTestId('pantry-empty-add'));
    expect(screen).toHavePathname('/add');
  });

  test('an Add item button is always visible and opens /add', async () => {
    server.use(pantryHandler(testPantry([BUTTER, MILK])));
    await renderPantry();
    await screen.findByTestId(rowTestId(MILK));

    await press(screen.getByTestId('add-item-button'));
    expect(screen).toHavePathname('/add');
  });

  test('a failed load shows the user-facing message with Retry', async () => {
    let fail = true;
    server.use(
      http.get(apiUrl('/households/:householdId/pantry'), () =>
        fail ? HttpResponse.json({ error: { code: 'internal', message: 'boom' } }, { status: 500 }) : HttpResponse.json(testPantry([MILK])),
      ),
    );
    await renderPantry();

    const error = await screen.findByTestId('pantry-error');
    expect(within(error).getByText('The server ran into a problem. Please try again in a moment.')).toBeTruthy();
    expect(screen.queryByText('Your pantry is empty')).toBeNull();
    expect(screen.getByTestId('add-item-button')).toBeTruthy();

    fail = false;
    await press(within(error).getByRole('button', { name: 'Retry' }));
    expect(await screen.findByTestId(rowTestId(MILK))).toBeTruthy();
    expect(screen.queryByTestId('pantry-error')).toBeNull();
  });

  test('a failed refetch keeps the list on screen with a non-blocking error', async () => {
    let fail = false;
    server.use(
      http.get(apiUrl('/households/:householdId/pantry'), () =>
        fail ? HttpResponse.error() : HttpResponse.json(testPantry([MILK])),
      ),
    );
    await renderPantry();
    await screen.findByTestId(rowTestId(MILK));

    fail = true;
    // Not awaited: the retries wait on (fake) timers, which findBy* advances.
    await act(async () => {
      void appQueryClient().refetchQueries({ queryKey: queryKeys.households.pantry(HOME.id) });
    });

    const banner = await screen.findByTestId('pantry-refresh-error');
    expect(within(banner).getByText(/Can't reach the server/)).toBeTruthy();
    expect(screen.getByTestId(rowTestId(MILK))).toBeTruthy();
    expect(screen.queryByTestId('pantry-error')).toBeNull();

    fail = false;
    await press(within(banner).getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.queryByTestId('pantry-refresh-error')).toBeNull());
    expect(screen.getByTestId(rowTestId(MILK))).toBeTruthy();
  });

  test('at 768 px and wider the list respects the content max width', async () => {
    server.use(pantryHandler(testPantry([MILK])));
    await setWindowWidth(1440);
    await renderPantry();
    await screen.findByTestId(rowTestId(MILK));

    const content = screen.getByTestId('screen-content');
    expect(content).toHaveStyle({ maxWidth: CONTENT_MAX_WIDTH });
    expect(within(content).getByTestId('pantry-list')).toBeTruthy();
    expect(within(content).getByTestId('add-item-button')).toBeTruthy();
  });
});

describe('view toggle and filters', () => {
  const FRIDGE = 1;
  const FREEZER = 2;
  const PANTRY = 3;
  // This file's own categories, so the tests do not depend on the shared fixture's names.
  const CATEGORIES = [
    { id: 1, key: 'produce', name: 'Produce' },
    { id: 2, key: 'dairy', name: 'Dairy' },
    { id: 4, key: 'grains', name: 'Grains' },
  ];
  const PRODUCE = 1;
  const DAIRY = 2;
  const GRAINS = 4;

  const SPLIT_FRIDGE_OLD = testEntry({ locationId: FRIDGE, expiresOn: '2026-10-25', expiryStatus: 'expired' });
  const SPLIT_FRIDGE_NEW = testEntry({ locationId: FRIDGE, expiresOn: '2026-12-25' });
  const SPLIT_FREEZER_A = testEntry({ locationId: FREEZER, expiresOn: '2027-01-10' });
  const SPLIT_FREEZER_B = testEntry({ locationId: FREEZER, expiresOn: '2027-02-10' });
  const CHEESE = testGroup('Cheese', [SPLIT_FRIDGE_OLD, SPLIT_FRIDGE_NEW, SPLIT_FREEZER_A, SPLIT_FREEZER_B], {
    categoryId: DAIRY,
  });
  const PEAS = testGroup('Peas', [testEntry({ locationId: FREEZER })], { categoryId: PRODUCE });
  const RICE = testGroup('Rice', [testEntry({ locationId: PANTRY })], { categoryId: GRAINS });
  const YOGURT = testGroup('Yogurt', [testEntry({ locationId: FRIDGE })], { categoryId: DAIRY });
  const GROUPS = [CHEESE, PEAS, RICE, YOGURT];

  beforeEach(() => {
    server.use(categoriesHandler(CATEGORIES));
  });

  /** The visible section titles and their counts, top to bottom: ["Fridge 2", …]. */
  function sectionOutline() {
    return screen.getAllByTestId(/^pantry-section-(location|category|unknown)/).map((header) => {
      const count = within(header).getByTestId('pantry-section-count').props.children;
      const title = String(header.props['aria-label']).split(',')[0];
      return `${title} ${count}`;
    });
  }

  /** Records the path of every API request from now on. */
  function recordRequests() {
    const paths: string[] = [];
    const listener = (event: { request: Request }) => {
      paths.push(new URL(event.request.url).pathname);
    };
    server.events.on('request:start', listener);
    return {
      paths,
      stop: () => server.events.removeListener('request:start', listener),
    };
  }

  async function savedChoices() {
    return JSON.parse((await AsyncStorage.getItem(pantryViewKey(uid))) as string);
  }

  async function saveChoices(choices: { mode: string; locationIds: number[]; categoryIds: number[]; expiring?: boolean }) {
    await AsyncStorage.setItem(pantryViewKey(uid), JSON.stringify(choices));
  }

  test('a new install groups by location, with a "Group by" control and section headers with counts', async () => {
    server.use(pantryHandler(testPantry(GROUPS)));
    await renderPantry();

    expect(await screen.findByTestId('pantry-section-location-1')).toBeTruthy();
    const groupBy = screen.getByLabelText('Group by');
    const location = within(groupBy).getByRole('radio', { name: 'Location' });
    const category = within(groupBy).getByRole('radio', { name: 'Category' });
    expect(location.props.accessibilityState).toMatchObject({ checked: true, selected: true });
    expect(category.props.accessibilityState).toMatchObject({ checked: false, selected: false });
    expect(location).toHaveStyle({ backgroundColor: colors.light.surface });

    // Fridge, Freezer, Pantry: the locations' order. Cheese is in two sections.
    expect(sectionOutline()).toEqual(['Fridge 2', 'Freezer 2', 'Pantry 1']);
    const fridge = screen.getByTestId('pantry-section-location-1');
    expect(fridge.props.role).toBe('heading');
    expect(fridge.props['aria-label']).toBe('Fridge, 2 items');
    expect(StyleSheet.flatten(within(fridge).getByText('Fridge').props.style)).toMatchObject({
      textTransform: 'uppercase',
      color: colors.light.mutedText,
    });
    expect(within(screen.getByTestId('pantry-section-location-3')).getByTestId('pantry-section-count')).toHaveTextContent(
      '1',
    );
  });

  test('by location, an item in two locations has a row in each with only those entries, highlighted per section', async () => {
    server.use(pantryHandler(testPantry(GROUPS)));
    await renderPantry();

    const fridgeRow = await screen.findByTestId(rowTestId(CHEESE, 'location-1'));
    const freezerRow = screen.getByTestId(rowTestId(CHEESE, 'location-2'));
    expect(within(fridgeRow).getByTestId('pantry-row-count')).toHaveTextContent('×2');
    expect(within(freezerRow).getByTestId('pantry-row-count')).toHaveTextContent('×2');
    // Expired in the Fridge only.
    expect(fridgeRow).toHaveStyle({ borderColor: colors.light.danger });
    expect(freezerRow).toHaveStyle({ borderColor: colors.light.border });

    // Expanding the Fridge row leaves the Freezer row collapsed.
    await press(within(fridgeRow).getByRole('button', { name: /^Cheese, 2 entries/ }));
    expect(screen.getByTestId(`pantry-entry-${SPLIT_FRIDGE_OLD.id}`)).toBeTruthy();
    expect(screen.getByTestId(`pantry-entry-${SPLIT_FRIDGE_NEW.id}`)).toBeTruthy();
    expect(screen.queryByTestId(`pantry-entry-${SPLIT_FREEZER_A.id}`)).toBeNull();
    const freezerButton = within(screen.getByTestId(rowTestId(CHEESE, 'location-2'))).getByRole('button', {
      name: /^Cheese, 2 entries/,
    });
    expect(freezerButton.props.accessibilityState).toMatchObject({ expanded: false });
  });

  test('a single entry left in a location is a plain row with no count, and opens the entry', async () => {
    const fridge = testEntry({ locationId: FRIDGE });
    const milk = testGroup('Milk', [fridge, testEntry({ locationId: FREEZER })]);
    server.use(pantryHandler(testPantry([milk])));
    await renderPantry();

    const row = await screen.findByTestId(rowTestId(milk, 'location-1'));
    expect(screen.getByTestId(rowTestId(milk, 'location-2'))).toBeTruthy();
    expect(within(row).queryByTestId('pantry-row-count')).toBeNull();
    await press(within(row).getByText('Milk'));
    expect(screen).toHavePathname(`/entries/${fridge.id}`);
  });

  test('switching to Category re-sections without a request, collapses rows, and is saved', async () => {
    server.use(pantryHandler(testPantry(GROUPS)));
    await renderPantry();
    const fridgeRow = await screen.findByTestId(rowTestId(CHEESE, 'location-1'));
    await press(within(fridgeRow).getByRole('button', { name: /^Cheese, 2 entries/ }));
    expect(screen.getByTestId(`pantry-entry-${SPLIT_FRIDGE_OLD.id}`)).toBeTruthy();

    const requests = recordRequests();
    await press(screen.getByRole('radio', { name: 'Category' }));
    requests.stop();

    expect(requests.paths).toEqual([]);
    expect(screen.getByRole('radio', { name: 'Category' }).props.accessibilityState).toMatchObject({ selected: true });
    expect(sectionOutline()).toEqual(['Produce 1', 'Dairy 2', 'Grains 1']);
    const cheese = screen.getByTestId(rowTestId(CHEESE, 'category-2'));
    expect(within(cheese).getByTestId('pantry-row-count')).toHaveTextContent('×4');
    expect(screen.queryByTestId(`pantry-entry-${SPLIT_FRIDGE_OLD.id}`)).toBeNull();
    expect((await savedChoices()).mode).toBe('category');

    await press(screen.getByRole('radio', { name: 'Location' }));
    expect(sectionOutline()).toEqual(['Fridge 2', 'Freezer 2', 'Pantry 1']);
    expect(screen.queryByTestId(`pantry-entry-${SPLIT_FRIDGE_OLD.id}`)).toBeNull();
  });

  test('filters: chips in a panel, a count on the button, "Filtered: …" with Clear, and no request', async () => {
    server.use(pantryHandler(testPantry(GROUPS)));
    await renderPantry();
    await screen.findByTestId('pantry-section-location-1');

    expect(screen.queryByTestId('pantry-filter-panel')).toBeNull();
    const requests = recordRequests();
    await press(screen.getByRole('button', { name: 'Filters' }));
    const panel = screen.getByTestId('pantry-filter-panel');
    const locationChips = within(within(panel).getByLabelText('Location')).getAllByRole('checkbox');
    const categoryChips = within(within(panel).getByLabelText('Category')).getAllByRole('checkbox');
    expect(locationChips.map((chip) => chip.props.accessibilityLabel)).toEqual(TEST_LOCATIONS.map((l) => l.name));
    expect(categoryChips.map((chip) => chip.props.accessibilityLabel)).toEqual(['Produce', 'Dairy', 'Grains']);
    expect(locationChips[0]).toHaveStyle({ backgroundColor: colors.light.surface, borderColor: colors.light.border });
    expect(StyleSheet.flatten(locationChips[0].props.style).minHeight).toBeGreaterThanOrEqual(44);

    // Fridge: only Fridge entries, so Cheese ×2 and Yogurt.
    await press(within(panel).getByRole('checkbox', { name: 'Fridge' }));
    const fridgeChip = within(screen.getByTestId('pantry-filter-panel')).getByRole('checkbox', { name: 'Fridge' });
    expect(fridgeChip.props.accessibilityState).toMatchObject({ checked: true, selected: true });
    expect(fridgeChip).toHaveStyle({ backgroundColor: colors.light.primarySubtle, borderColor: colors.light.primary });
    expect(sectionOutline()).toEqual(['Fridge 2']);
    expect(screen.getByRole('button', { name: 'Filters · 1' })).toBeTruthy();

    // Freezer as well: either location.
    await press(screen.getByRole('checkbox', { name: 'Freezer' }));
    expect(sectionOutline()).toEqual(['Fridge 2', 'Freezer 2']);

    // And Dairy: location and category must both match.
    await press(screen.getByRole('checkbox', { name: 'Dairy' }));
    expect(sectionOutline()).toEqual(['Fridge 2', 'Freezer 1']);
    expect(within(screen.getByTestId('pantry-filter-line')).getByText('Filtered: Fridge, Freezer, Dairy')).toBeTruthy();

    // The line stays when the panel is closed.
    await press(screen.getByRole('button', { name: 'Filters · 3' }));
    expect(screen.queryByTestId('pantry-filter-panel')).toBeNull();
    const line = screen.getByTestId('pantry-filter-line');
    expect(within(line).getByText('Filtered: Fridge, Freezer, Dairy')).toBeTruthy();
    expect(await savedChoices()).toEqual({ mode: 'location', locationIds: [FRIDGE, FREEZER], categoryIds: [DAIRY], expiring: false });

    await press(within(line).getByRole('button', { name: 'Clear' }));
    requests.stop();
    expect(screen.queryByTestId('pantry-filter-line')).toBeNull();
    expect(screen.getByRole('button', { name: 'Filters' })).toBeTruthy();
    expect(sectionOutline()).toEqual(['Fridge 2', 'Freezer 2', 'Pantry 1']);
    expect(requests.paths).toEqual([]);
  });

  test('a location filter splits a group in the category view too: one Milk in the Fridge is a single entry', async () => {
    const fridge = testEntry({ locationId: FRIDGE });
    const milk = testGroup('Milk', [fridge, testEntry({ locationId: FREEZER })], { categoryId: DAIRY });
    server.use(pantryHandler(testPantry([milk])));
    await saveChoices({ mode: 'category', locationIds: [FRIDGE], categoryIds: [] });
    await renderPantry();

    const row = await screen.findByTestId(rowTestId(milk, 'category-2'));
    expect(within(row).queryByTestId('pantry-row-count')).toBeNull();
    expect(within(row).getByText('Bought Oct 20 · Fridge')).toBeTruthy();
  });

  test('filters that match nothing show "No items match these filters" with Clear filters', async () => {
    server.use(pantryHandler(testPantry([YOGURT])));
    await renderPantry();
    await screen.findByTestId('pantry-section-location-1');

    await press(screen.getByRole('button', { name: 'Filters' }));
    await press(screen.getByRole('checkbox', { name: 'Grains' }));

    const empty = screen.getByTestId('pantry-no-matches');
    expect(within(empty).getByText('No items match these filters')).toBeTruthy();
    expect(screen.queryByText('Your pantry is empty')).toBeNull();
    expect(screen.queryAllByTestId(/^pantry-row-/)).toHaveLength(0);
    // The controls stay above it, so the filters can still be changed.
    expect(within(screen.getByTestId('pantry-list')).getByTestId('pantry-filter-panel')).toBeTruthy();

    await press(within(empty).getByRole('button', { name: 'Clear filters' }));
    expect(screen.queryByTestId('pantry-no-matches')).toBeNull();
    expect(screen.getByText('Yogurt')).toBeTruthy();
  });

  test('an empty pantry shows the empty state with no controls, and keeps the remembered choices', async () => {
    await saveChoices({ mode: 'category', locationIds: [FRIDGE], categoryIds: [] });
    const saved = await AsyncStorage.getItem(pantryViewKey(uid));
    await renderPantry();

    expect(await screen.findByText('Your pantry is empty')).toBeTruthy();
    expect(screen.queryByLabelText('Group by')).toBeNull();
    expect(screen.queryByTestId('pantry-filters-button')).toBeNull();
    expect(screen.queryByTestId('pantry-filter-line')).toBeNull();
    expect(await AsyncStorage.getItem(pantryViewKey(uid))).toBe(saved);
  });

  test('restores the saved choices without showing the default view first; unknown IDs are ignored and dropped on the next save', async () => {
    await saveChoices({ mode: 'category', locationIds: [FRIDGE, 99], categoryIds: [77] });
    server.use(pantryHandler(testPantry(GROUPS)));
    await renderPantry();

    // Every render until the category view appears shows the loading placeholder, never the location view.
    await waitFor(() => {
      expect(screen.queryByTestId('pantry-section-location-1')).toBeNull();
      expect(screen.queryByTestId('pantry-section-category-2')).not.toBeNull();
    });
    expect(screen.getByRole('radio', { name: 'Category' }).props.accessibilityState).toMatchObject({ selected: true });
    expect(screen.getByText('Filtered: Fridge')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Filters · 1' })).toBeTruthy();
    expect(sectionOutline()).toEqual(['Dairy 2']);

    await press(screen.getByRole('radio', { name: 'Location' }));
    expect(await savedChoices()).toEqual({ mode: 'location', locationIds: [FRIDGE], categoryIds: [], expiring: false });
  });

  test('malformed saved data falls back to Location with no filters, without an error', async () => {
    await AsyncStorage.setItem(pantryViewKey(uid), '{not json');
    server.use(pantryHandler(testPantry(GROUPS)));
    await renderPantry();

    expect(await screen.findByTestId('pantry-section-location-1')).toBeTruthy();
    expect(screen.queryByTestId('pantry-filter-line')).toBeNull();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  test('another user\'s choices on the same device do not apply', async () => {
    await AsyncStorage.setItem(
      pantryViewKey('someone-else'),
      JSON.stringify({ mode: 'category', locationIds: [FRIDGE], categoryIds: [] }),
    );
    server.use(pantryHandler(testPantry(GROUPS)));
    await renderPantry();

    expect(await screen.findByTestId('pantry-section-location-1')).toBeTruthy();
    expect(screen.queryByTestId('pantry-filter-line')).toBeNull();
  });

  test('a refetch keeps the view and filters, and a moved entry changes section', async () => {
    const entry = testEntry({ locationId: FRIDGE });
    const milk = testGroup('Milk', [entry], { categoryId: DAIRY });
    server.use(pantryHandler(testPantry([milk, YOGURT, RICE])));
    await saveChoices({ mode: 'location', locationIds: [], categoryIds: [DAIRY] });
    await renderPantry();
    expect(await screen.findByTestId(rowTestId(milk, 'location-1'))).toBeTruthy();
    expect(sectionOutline()).toEqual(['Fridge 2']);

    const moved = { ...milk, entries: [{ ...milk.entries[0], locationId: FREEZER }] };
    server.use(pantryHandler(testPantry([moved, YOGURT, RICE])));
    await act(async () => {
      await appQueryClient().refetchQueries({ queryKey: queryKeys.households.pantry(HOME.id) });
    });

    expect(await screen.findByTestId(rowTestId(milk, 'location-2'))).toBeTruthy();
    expect(screen.queryByTestId(rowTestId(milk, 'location-1'))).toBeNull();
    expect(sectionOutline()).toEqual(['Fridge 1', 'Freezer 1']);
    expect(screen.getByText('Filtered: Dairy')).toBeTruthy();
  });

  test('an entry or item whose location or category is unknown goes in a final "Unknown" section', async () => {
    const odd = testGroup('Odd', [testEntry({ locationId: 42 })], { categoryId: 42 });
    server.use(pantryHandler(testPantry([odd, YOGURT])));
    await renderPantry();

    await screen.findByTestId('pantry-section-unknown');
    expect(sectionOutline()).toEqual(['Fridge 1', 'Unknown 1']);
    await press(screen.getByRole('radio', { name: 'Category' }));
    expect(sectionOutline()).toEqual(['Dairy 1', 'Unknown 1']);
  });

  test('while categories first load, the loading placeholder shows', async () => {
    let release: () => void = () => {};
    const held = new Promise<void>((resolve) => {
      release = resolve;
    });
    server.use(
      pantryHandler(testPantry(GROUPS)),
      http.get(apiUrl('/categories'), async () => {
        await held;
        return HttpResponse.json({ items: CATEGORIES });
      }),
    );
    await renderPantry();

    expect(await screen.findByTestId('pantry-loading')).toBeTruthy();
    expect(screen.queryByTestId('pantry-list')).toBeNull();
    await act(async () => {
      release();
    });
    expect(await screen.findByTestId('pantry-section-location-1')).toBeTruthy();
  });

  test('a failing categories request shows the error state, and Retry refetches it', async () => {
    let fail = true;
    let categoryRequests = 0;
    server.use(
      pantryHandler(testPantry(GROUPS)),
      http.get(apiUrl('/categories'), () => {
        categoryRequests += 1;
        return fail
          ? HttpResponse.json({ error: { code: 'internal', message: 'boom' } }, { status: 500 })
          : HttpResponse.json({ items: CATEGORIES });
      }),
    );
    await renderPantry();

    const error = await screen.findByTestId('pantry-error');
    expect(within(error).getByText('The server ran into a problem. Please try again in a moment.')).toBeTruthy();
    expect(screen.queryByTestId('pantry-list')).toBeNull();

    fail = false;
    const before = categoryRequests;
    await press(within(error).getByRole('button', { name: 'Retry' }));
    expect(await screen.findByTestId('pantry-section-location-1')).toBeTruthy();
    expect(categoryRequests).toBeGreaterThan(before);
  });

  test('a failing locations request shows the error state too', async () => {
    server.use(
      pantryHandler(testPantry(GROUPS)),
      http.get(apiUrl('/locations'), () =>
        HttpResponse.json({ error: { code: 'internal', message: 'boom' } }, { status: 500 }),
      ),
    );
    await renderPantry();

    const error = await screen.findByTestId('pantry-error');
    server.use(locationsHandler());
    await press(within(error).getByRole('button', { name: 'Retry' }));
    expect(await screen.findByTestId('pantry-section-location-1')).toBeTruthy();
  });

  test('the controls and an open panel scroll with the list, and the content clears the floating Add item button', async () => {
    await setWindowWidth(375);
    server.use(pantryHandler(testPantry(GROUPS)));
    await renderPantry();
    await screen.findByTestId('pantry-section-location-1');
    await press(screen.getByRole('button', { name: 'Filters' }));

    // Toolbar, panel and filter line are the list's header, not a fixed block above it,
    // so on a short phone the last chip and the rows below are reached by scrolling.
    const list = screen.getByTestId('pantry-list');
    expect(within(list).getByLabelText('Group by')).toBeTruthy();
    expect(within(list).getByTestId('pantry-filter-panel')).toBeTruthy();
    await press(within(list).getByRole('checkbox', { name: 'Fridge' }));
    expect(within(screen.getByTestId('pantry-list')).getByTestId('pantry-filter-line')).toBeTruthy();
    expect(within(screen.getByTestId('pantry-list')).getByTestId(rowTestId(YOGURT, 'location-1'))).toBeTruthy();
    expect(StyleSheet.flatten(screen.getByTestId('pantry-list').props.contentContainerStyle).paddingBottom).toBeGreaterThanOrEqual(
      96,
    );
  });

  test('at 768 px and wider the Group by control is sized to its options', async () => {
    server.use(pantryHandler(testPantry(GROUPS)));
    await setWindowWidth(1440);
    await renderPantry();
    await screen.findByTestId('pantry-section-location-1');

    expect(StyleSheet.flatten(screen.getByTestId('pantry-group-by').props.style)).toMatchObject({ flexGrow: 0 });
  });

  test('the chips wrap onto several lines instead of scrolling sideways', async () => {
    server.use(pantryHandler(testPantry(GROUPS)));
    await renderPantry();
    await screen.findByTestId('pantry-section-location-1');
    await press(screen.getByRole('button', { name: 'Filters' }));

    const panel = screen.getByTestId('pantry-filter-panel');
    for (const title of ['Location', 'Category']) {
      const chip = within(within(panel).getByLabelText(title)).getAllByRole('checkbox')[0];
      expect(StyleSheet.flatten(chip.parent?.props.style)).toMatchObject({ flexDirection: 'row', flexWrap: 'wrap' });
    }
  });

  test('at 768 px and wider the controls sit above the list with the inline Add item button', async () => {
    server.use(pantryHandler(testPantry(GROUPS)));
    await setWindowWidth(1440);
    await renderPantry();
    await screen.findByTestId('pantry-section-location-1');

    const content = screen.getByTestId('screen-content');
    expect(within(content).getByLabelText('Group by')).toBeTruthy();
    expect(within(content).getByTestId('pantry-filters-button')).toBeTruthy();
    expect(within(content).getByTestId('add-item-button')).toBeTruthy();
  });

  describe('Expiring chip and summary card', () => {
    const MILK_SOON = testEntry({ locationId: FRIDGE, expiresOn: '2026-11-03', expiryStatus: 'soon' });
    const MILK_FRESH = testEntry({ locationId: FRIDGE, expiresOn: '2026-12-20' });
    const MILK_E = testGroup('Milk', [MILK_SOON, MILK_FRESH], { categoryId: DAIRY });
    const BREAD_E = testGroup('Bread', [testEntry({ locationId: PANTRY, expiresOn: '2026-10-30', expiryStatus: 'expired' })], {
      categoryId: GRAINS,
    });
    // Out, so never counted even though its status says soon.
    const KALE_OUT = testGroup('Kale', [testEntry({ locationId: FRIDGE, level: 'out', expiresOn: '2026-11-03', expiryStatus: 'soon' })], {
      categoryId: PRODUCE,
    });
    const YOGURT_E = testGroup('Yogurt', [testEntry({ locationId: FREEZER, expiresOn: '2026-11-04', expiryStatus: 'soon' })], {
      categoryId: DAIRY,
    });
    // The API's order (by name): Bread, Kale, Milk, Peas, Yogurt.
    const EXPIRING_GROUPS = [BREAD_E, KALE_OUT, MILK_E, PEAS, YOGURT_E];

    test('"Expiring N" sits next to Group by and counts entries (not out) that are soon or expired', async () => {
      server.use(pantryHandler(testPantry(EXPIRING_GROUPS)));
      await renderPantry();
      await screen.findByTestId('pantry-section-location-1');

      const chip = screen.getByRole('checkbox', { name: 'Expiring 3' });
      expect(within(chip).getByText('Expiring 3')).toBeTruthy();
      // In the toolbar, next to Group by, wrapping together with Filters (#126).
      const actions = screen.getByTestId('pantry-toolbar-actions');
      expect(within(actions).getByTestId('pantry-expiring-chip')).toBeTruthy();
      expect(within(actions).getByTestId('pantry-filters-button')).toBeTruthy();
      expect(actions.parent).toBe(screen.getByTestId('pantry-group-by').parent);
      expect(chip.props.accessibilityState).toMatchObject({ checked: false });
      expect(chip).toHaveStyle({ backgroundColor: colors.light.expiringSubtle });
      expect(StyleSheet.flatten(within(chip).getByText('Expiring 3').props.style).color).toBe(colors.light.expiringSoon);
      expect(StyleSheet.flatten(chip.props.style).minHeight).toBeGreaterThanOrEqual(44);
    });

    test('turning the chip on shows only those entries, says "Filtered: Expiring", and Clear turns it off', async () => {
      server.use(pantryHandler(testPantry(EXPIRING_GROUPS)));
      await renderPantry();
      await screen.findByTestId('pantry-section-location-1');

      const requests = recordRequests();
      await press(screen.getByRole('checkbox', { name: 'Expiring 3' }));
      const chip = screen.getByRole('checkbox', { name: 'Expiring 3' });
      expect(chip.props.accessibilityState).toMatchObject({ checked: true, selected: true });
      expect(chip).toHaveStyle({ backgroundColor: colors.light.expiringSoon });

      // Milk keeps only its soon entry (a plain row); Kale (out) and Peas are dropped.
      expect(sectionOutline()).toEqual(['Fridge 1', 'Freezer 1', 'Pantry 1']);
      const milk = screen.getByTestId(rowTestId(MILK_E, 'location-1'));
      expect(within(milk).queryByTestId('pantry-row-count')).toBeNull();
      expect(within(milk).getByText('Expires tomorrow')).toBeTruthy();
      expect(screen.queryByText('Kale')).toBeNull();
      expect(screen.queryByText('Peas')).toBeNull();
      expect(within(screen.getByTestId('pantry-filter-line')).getByText('Filtered: Expiring')).toBeTruthy();
      // The panel's count is unchanged: the chip is not in the panel.
      expect(screen.getByRole('button', { name: 'Filters' })).toBeTruthy();
      expect(screen.queryByTestId('pantry-expiring-card')).toBeNull();
      expect(await savedChoices()).toEqual({ mode: 'location', locationIds: [], categoryIds: [], expiring: true });

      await press(within(screen.getByTestId('pantry-filter-line')).getByRole('button', { name: 'Clear' }));
      requests.stop();
      expect(requests.paths).toEqual([]);
      expect(screen.getByRole('checkbox', { name: 'Expiring 3' }).props.accessibilityState).toMatchObject({ checked: false });
      expect(screen.queryByTestId('pantry-filter-line')).toBeNull();
      expect(screen.getByTestId('pantry-expiring-card')).toBeTruthy();
      expect(sectionOutline()).toEqual(['Fridge 2', 'Freezer 2', 'Pantry 1']);
      expect((await savedChoices()).expiring).toBe(false);
    });

    test('the chip combines with a location filter: both must match', async () => {
      server.use(pantryHandler(testPantry(EXPIRING_GROUPS)));
      await renderPantry();
      await screen.findByTestId('pantry-section-location-1');

      await press(screen.getByRole('checkbox', { name: 'Expiring 3' }));
      await press(screen.getByRole('button', { name: 'Filters' }));
      await press(within(screen.getByTestId('pantry-filter-panel')).getByRole('checkbox', { name: 'Fridge' }));

      expect(sectionOutline()).toEqual(['Fridge 1']);
      expect(screen.getByTestId(rowTestId(MILK_E, 'location-1'))).toBeTruthy();
      expect(within(screen.getByTestId('pantry-filter-line')).getByText('Filtered: Expiring, Fridge')).toBeTruthy();
      expect(screen.getByRole('button', { name: 'Filters · 1' })).toBeTruthy();
      expect(await savedChoices()).toEqual({ mode: 'location', locationIds: [FRIDGE], categoryIds: [], expiring: true });

      // A category with nothing expiring matches nothing; Clear filters turns the chip off too.
      await press(within(screen.getByTestId('pantry-filter-panel')).getByRole('checkbox', { name: 'Fridge' }));
      await press(within(screen.getByTestId('pantry-filter-panel')).getByRole('checkbox', { name: 'Produce' }));
      expect(screen.getByTestId('pantry-no-matches')).toBeTruthy();
      await press(within(screen.getByTestId('pantry-no-matches')).getByRole('button', { name: 'Clear filters' }));
      expect(screen.getByRole('checkbox', { name: 'Expiring 3' }).props.accessibilityState).toMatchObject({ checked: false });
      expect(sectionOutline()).toEqual(['Fridge 2', 'Freezer 2', 'Pantry 1']);
    });

    test('the summary card says how many are expiring, lists the names on at most two lines, and turns the chip on', async () => {
      server.use(pantryHandler(testPantry(EXPIRING_GROUPS)));
      await renderPantry();
      await screen.findByTestId('pantry-section-location-1');

      const card = screen.getByTestId('pantry-expiring-card');
      expect(within(screen.getByTestId('pantry-list')).getByTestId('pantry-expiring-card')).toBe(card);
      expect(within(card).getByText('3 items expiring soon')).toBeTruthy();
      const names = within(card).getByTestId('pantry-expiring-card-names');
      expect(names).toHaveTextContent('Bread, Milk, Yogurt');
      expect(names.props.numberOfLines).toBe(2);
      expect(card.props.accessibilityLabel).toBe('3 items expiring soon: Bread, Milk, Yogurt');
      expect(card).toHaveStyle({ backgroundColor: colors.light.expiringSubtle, borderColor: colors.light.expiringSoon });
      expect(StyleSheet.flatten(within(card).getByText('3 items expiring soon').props.style).color).toBe(
        colors.light.expiringSoon,
      );

      await press(card);
      expect(screen.getByRole('checkbox', { name: 'Expiring 3' }).props.accessibilityState).toMatchObject({ checked: true });
      expect(screen.queryByTestId('pantry-expiring-card')).toBeNull();
      expect(sectionOutline()).toEqual(['Fridge 1', 'Freezer 1', 'Pantry 1']);
    });

    test('one expiring entry reads "1 item expiring soon"', async () => {
      server.use(pantryHandler(testPantry([BREAD_E, PEAS])));
      await renderPantry();

      expect(await screen.findByText('1 item expiring soon')).toBeTruthy();
      expect(screen.getByRole('checkbox', { name: 'Expiring 1' })).toBeTruthy();
    });

    test('with nothing expiring (N = 0) neither the chip nor the card shows, and a saved Expiring is ignored', async () => {
      await saveChoices({ mode: 'location', locationIds: [], categoryIds: [], expiring: true });
      server.use(pantryHandler(testPantry([KALE_OUT, PEAS, RICE])));
      await renderPantry();
      await screen.findByTestId('pantry-section-location-1');

      expect(screen.queryByTestId('pantry-expiring-chip')).toBeNull();
      expect(screen.queryByTestId('pantry-expiring-card')).toBeNull();
      expect(screen.queryByTestId('pantry-filter-line')).toBeNull();
      expect(sectionOutline()).toEqual(['Fridge 1', 'Freezer 1', 'Pantry 1']);
      // Group by and Filters, with no chip between them.
      expect(within(screen.getByTestId('pantry-toolbar-actions')).getByTestId('pantry-filters-button')).toBeTruthy();
    });

    describe('the #110 QA seed: Milk expiring in the Fridge and the Freezer, plus six other items', () => {
      // In the API's order (by name). TEST_TODAY is 2 Nov.
      const MILK_FRIDGE = testEntry({ locationId: FRIDGE, expiresOn: '2026-11-04', expiryStatus: 'soon' });
      const MILK_FREEZER = testEntry({ locationId: FREEZER, expiresOn: '2026-11-03', expiryStatus: 'soon' });
      const SEED = [
        testGroup('Apples', [testEntry({ locationId: PANTRY, expiresOn: '2026-11-05', expiryStatus: 'soon' })]),
        testGroup('Bread', [testEntry({ locationId: PANTRY, expiresOn: '2026-10-30', expiryStatus: 'expired' })], {
          categoryId: GRAINS,
        }),
        testGroup('Chicken', [testEntry({ locationId: FRIDGE, expiresOn: '2026-11-02', expiryStatus: 'soon' })]),
        testGroup('Eggs', [testEntry({ locationId: FRIDGE, expiresOn: '2026-11-03', expiryStatus: 'soon' })]),
        testGroup('Milk', [MILK_FRIDGE, MILK_FREEZER], { categoryId: DAIRY }),
        PEAS,
        testGroup('Spinach', [testEntry({ locationId: FRIDGE, expiresOn: '2026-11-01', expiryStatus: 'expired' })], {
          categoryId: PRODUCE,
        }),
        testGroup('Yogurt', [testEntry({ locationId: FRIDGE, expiresOn: '2026-11-05', expiryStatus: 'soon' })], {
          categoryId: DAIRY,
        }),
      ];
      const SOONEST_FIRST = 'Bread, Spinach, Chicken, Eggs, Milk, Apples, Yogurt';

      test('the chip says "Expiring 7" and the card "7 items expiring soon" above 7 names, soonest first', async () => {
        server.use(pantryHandler(testPantry(SEED)));
        await renderPantry();
        await screen.findByTestId('pantry-section-location-1');

        expect(screen.getByRole('checkbox', { name: 'Expiring 7' })).toBeTruthy();
        const card = screen.getByTestId('pantry-expiring-card');
        expect(within(card).getByText('7 items expiring soon')).toBeTruthy();
        // Milk's soonest entry (3 Nov, in the Freezer) places it; Eggs keeps its API place on the tie.
        expect(within(card).getByTestId('pantry-expiring-card-names')).toHaveTextContent(SOONEST_FIRST);
        expect(card.props.accessibilityLabel).toBe(`7 items expiring soon: ${SOONEST_FIRST}`);
      });

      test('turning the chip on still shows every expiring entry: Milk in the Fridge and the Freezer', async () => {
        server.use(pantryHandler(testPantry(SEED)));
        await renderPantry();
        await screen.findByTestId('pantry-section-location-1');

        await press(screen.getByRole('checkbox', { name: 'Expiring 7' }));
        expect(screen.getByRole('checkbox', { name: 'Expiring 7' }).props.accessibilityState).toMatchObject({ checked: true });
        expect(screen.getByTestId(rowTestId(SEED[4], 'location-1'))).toBeTruthy();
        expect(screen.getByTestId(rowTestId(SEED[4], 'location-2'))).toBeTruthy();
        // Eight rows for seven items: Milk is in two sections. Peas (fresh) is dropped.
        expect(sectionOutline()).toEqual(['Fridge 5', 'Freezer 1', 'Pantry 2']);
        expect(screen.queryByText('Peas')).toBeNull();
      });
    });

    test('with 18 expiring items, the card\'s two lines start with the most urgent', async () => {
      // Named so the API's order (by name) is the reverse of the urgency order.
      const groups = Array.from({ length: 18 }, (_, index) => {
        const day = 18 - index; // Item 00 expires last (Nov 18), Item 17 first (Nov 1, expired).
        const expiresOn = `2026-11-${String(day).padStart(2, '0')}`;
        return testGroup(`Item ${String(index).padStart(2, '0')}`, [
          testEntry({ locationId: FRIDGE, expiresOn, expiryStatus: day < 2 ? 'expired' : 'soon' }),
        ]);
      });
      server.use(pantryHandler(testPantry(groups)));
      await renderPantry();

      const card = await screen.findByTestId('pantry-expiring-card');
      expect(within(card).getByText('18 items expiring soon')).toBeTruthy();
      const names = within(card).getByTestId('pantry-expiring-card-names');
      expect(names.props.numberOfLines).toBe(2);
      // The ellipsis cuts the end of the text, which is now the least urgent.
      const expected = groups.map((group) => group.name).reverse().join(', ');
      expect(names).toHaveTextContent(expected);
      expect(String(names.props.children).startsWith('Item 17, Item 16, Item 15')).toBe(true);
    });

    describe('a saved Expiring with nothing expiring', () => {
      // An unknown location ID (99) too: only Expiring is saved back, the rest stays as saved.
      const SAVED = { mode: 'category', locationIds: [FREEZER, 99], categoryIds: [DAIRY], expiring: true };
      const NOTHING_EXPIRING = [KALE_OUT, PEAS, RICE];

      /** Lets the screen's save-back effect and the (mocked) storage write finish. */
      async function flushSave() {
        await act(async () => {
          await new Promise<void>((resolve) => setImmediate(() => resolve()));
        });
      }

      test('is saved back as off once the pantry loads; when something starts expiring the chip shows off', async () => {
        await saveChoices(SAVED);
        let groups = NOTHING_EXPIRING;
        server.use(http.get(apiUrl('/households/:householdId/pantry'), () => HttpResponse.json(testPantry(groups))));
        await renderPantry();
        await screen.findByTestId('pantry-no-matches');

        await flushSave();

        expect((await savedChoices()).expiring).toBe(false);
        expect(await savedChoices()).toEqual({ ...SAVED, expiring: false });

        groups = [...NOTHING_EXPIRING, YOGURT_E];
        await act(async () => {
          await appQueryClient().refetchQueries({ queryKey: queryKeys.households.pantry(HOME.id) });
        });
        const chip = await screen.findByRole('checkbox', { name: 'Expiring 1' });
        expect(chip.props.accessibilityState).toMatchObject({ checked: false });
        expect(screen.getByTestId('pantry-expiring-card')).toBeTruthy();
        expect(screen.getByText('Filtered: Freezer, Dairy')).toBeTruthy();
        expect(await savedChoices()).toEqual({ ...SAVED, expiring: false });
      });

      test('an empty pantry counts as nothing expiring', async () => {
        await saveChoices(SAVED);
        server.use(pantryHandler(testPantry([])));
        await renderPantry();
        await screen.findByText('Your pantry is empty');

        await flushSave();

        expect(await savedChoices()).toEqual({ ...SAVED, expiring: false });
      });

      test('is left alone while the pantry is still loading', async () => {
        await saveChoices(SAVED);
        let release: () => void = () => {};
        const held = new Promise<void>((resolve) => {
          release = resolve;
        });
        server.use(
          http.get(apiUrl('/households/:householdId/pantry'), async () => {
            await held;
            return HttpResponse.json(testPantry(NOTHING_EXPIRING));
          }),
        );
        await renderPantry();
        expect(await screen.findByTestId('pantry-loading')).toBeTruthy();
        // Let the saved choices and the locations and categories load (setImmediate is not faked).
        for (let i = 0; i < 10; i += 1) {
          await act(async () => {
            await new Promise<void>((resolve) => setImmediate(() => resolve()));
          });
        }
        expect(screen.getByTestId('pantry-loading')).toBeTruthy();
        expect(await savedChoices()).toEqual(SAVED);

        await act(async () => {
          release();
        });
        await screen.findByTestId('pantry-no-matches');
        await flushSave();
        expect((await savedChoices()).expiring).toBe(false);
      });

      test('is left alone when the pantry request fails', async () => {
        await saveChoices(SAVED);
        server.use(
          http.get(apiUrl('/households/:householdId/pantry'), () =>
            HttpResponse.json({ error: { code: 'internal', message: 'boom' } }, { status: 500 }),
          ),
        );
        await renderPantry();
        await screen.findByTestId('pantry-error');

        expect(await savedChoices()).toEqual(SAVED);
      });
    });

    test('the chip state is restored from storage with the other filters', async () => {
      await saveChoices({ mode: 'category', locationIds: [], categoryIds: [], expiring: true });
      server.use(pantryHandler(testPantry(EXPIRING_GROUPS)));
      await renderPantry();

      await waitFor(() => {
        expect(screen.queryByTestId('pantry-section-category-1')).toBeNull();
        expect(screen.queryByTestId('pantry-section-category-2')).not.toBeNull();
      });
      expect(screen.getByRole('checkbox', { name: 'Expiring 3' }).props.accessibilityState).toMatchObject({ checked: true });
      expect(sectionOutline()).toEqual(['Dairy 2', 'Grains 1']);
      expect(screen.getByText('Filtered: Expiring')).toBeTruthy();
      expect(screen.queryByTestId('pantry-expiring-card')).toBeNull();
    });
  });
});

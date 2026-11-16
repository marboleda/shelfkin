import type { QueryClient } from '@tanstack/react-query';
import { act, fireEvent, waitFor, within } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { http, HttpResponse } from 'msw';
import { Dimensions, StyleSheet } from 'react-native';

import { queryKeys } from '@/api/query-keys';
import { householdRowLabel } from '@/components/household-switcher';
import { colors, CONTENT_MAX_WIDTH } from '@/theme';
import { apiUrl, setupApiServer } from '../test-utils/api';
import { signedInApiHandlers, signInTestUser, testHousehold, testMe } from '../test-utils/auth';
import { pantryHandler, testEntry, testGroup, testPantry } from '../test-utils/pantry';

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

beforeEach(async () => {
  signInTestUser();
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

    const butterRow = await screen.findByTestId(`pantry-row-${BUTTER.catalogItemId}`);
    const milkRow = screen.getByTestId(`pantry-row-${MILK.catalogItemId}`);
    expect(within(milkRow).getByText('Milk')).toBeTruthy();
    expect(within(milkRow).getByTestId('pantry-row-count')).toHaveTextContent('×2');
    expect(within(butterRow).getByText('Butter')).toBeTruthy();
    expect(within(butterRow).queryByTestId('pantry-row-count')).toBeNull();
    expect(within(butterRow).queryByText(/×1/)).toBeNull();

    const names = screen.getAllByTestId(/^pantry-row-0/).map((row) => row.props.testID);
    expect(names).toEqual([`pantry-row-${BUTTER.catalogItemId}`, `pantry-row-${MILK.catalogItemId}`]);
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

    const rendered = screen.getAllByTestId(/^pantry-row-0/).length;
    expect(rendered).toBeGreaterThan(0);
    expect(rendered).toBeLessThan(60);
    expect(screen.queryByText('Item 199')).toBeNull();
  });

  test('a single entry shows its details directly and opens /entries/<id>', async () => {
    server.use(pantryHandler(testPantry([BUTTER])));
    await renderPantry();

    const row = await screen.findByTestId(`pantry-row-${BUTTER.catalogItemId}`);
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
    expect(screen.getByTestId(`pantry-row-${group.catalogItemId}`)).toHaveStyle({ borderColor: colors.light.expiringSoon });
  });

  test('expired says "Expired Oct 25" in the danger colour', async () => {
    const group = testGroup('Yogurt', [testEntry({ expiresOn: '2026-10-25', expiryStatus: 'expired' })]);
    server.use(pantryHandler(testPantry([group])));
    await renderPantry();

    expect(await screen.findByText('Expired Oct 25')).toBeTruthy();
    expect(textStyle('Expired Oct 25').color).toBe(colors.light.danger);
    expect(screen.getByTestId('expiry-chip-expired')).toHaveStyle({ backgroundColor: colors.light.dangerSubtle });
    expect(screen.getByTestId(`pantry-row-${group.catalogItemId}`)).toHaveStyle({ borderColor: colors.light.danger });
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
    expect(screen.getByTestId(`pantry-row-${group.catalogItemId}`)).toHaveStyle({ borderColor: colors.light.border });
  });

  test('a collapsed row is highlighted when any entry is soon or expired, with words', async () => {
    const group = testGroup('Milk', [
      testEntry({ expiresOn: '2026-12-01' }),
      testEntry({ expiresOn: '2026-10-25', expiryStatus: 'expired' }),
    ]);
    server.use(pantryHandler(testPantry([group])));
    await renderPantry();

    expect(await screen.findByText('Expired Oct 25')).toBeTruthy();
    expect(screen.getByTestId(`pantry-row-${group.catalogItemId}`)).toHaveStyle({ borderColor: colors.light.danger });
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
    await screen.findByText('Milk');

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
    expect(await screen.findByText('Milk')).toBeTruthy();
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
    await screen.findByText('Milk');

    fail = true;
    // Not awaited: the retries wait on (fake) timers, which findBy* advances.
    await act(async () => {
      void appQueryClient().refetchQueries({ queryKey: queryKeys.households.pantry(HOME.id) });
    });

    const banner = await screen.findByTestId('pantry-refresh-error');
    expect(within(banner).getByText(/Can't reach the server/)).toBeTruthy();
    expect(screen.getByText('Milk')).toBeTruthy();
    expect(screen.queryByTestId('pantry-error')).toBeNull();

    fail = false;
    await press(within(banner).getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.queryByTestId('pantry-refresh-error')).toBeNull());
    expect(screen.getByText('Milk')).toBeTruthy();
  });

  test('at 768 px and wider the list respects the content max width', async () => {
    server.use(pantryHandler(testPantry([MILK])));
    await setWindowWidth(1440);
    await renderPantry();
    await screen.findByText('Milk');

    const content = screen.getByTestId('screen-content');
    expect(content).toHaveStyle({ maxWidth: CONTENT_MAX_WIDTH });
    expect(within(content).getByTestId('pantry-list')).toBeTruthy();
    expect(within(content).getByTestId('add-item-button')).toBeTruthy();
  });
});

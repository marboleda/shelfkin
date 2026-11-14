import AsyncStorage from '@react-native-async-storage/async-storage';
import type { QueryClient } from '@tanstack/react-query';
import { act, fireEvent, waitFor, within } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { http, HttpResponse } from 'msw';
import { Dimensions } from 'react-native';

import { ApiError } from '@/api/errors';
import { queryKeys } from '@/api/query-keys';
import { focusElement } from '@/components/focus';
import { rememberedHouseholdKey } from '@/household/storage';
import { apiUrl, setupApiServer } from '../test-utils/api';
import { householdsHandler, signedInApiHandlers, signInTestUser, testHousehold } from '../test-utils/auth';

jest.setTimeout(120000);

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

// Record where the switcher moves focus to.
// Native behaviour: focus returns as soon as the list closes (see household-focus-test for web).
jest.mock('@/components/focus', () => ({ focusElement: jest.fn(), focusWaitsForModalDismiss: () => false }));
const mockFocus = focusElement as jest.MockedFunction<typeof focusElement>;

// Retry without delay, and keep the app's QueryClient to trigger household-scoped queries.
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

const HOME = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000a', name: 'Home', role: 'owner' });
const CABIN = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000b', name: 'Cabin', role: 'member' });
const LONG_NAME = 'The Very Long Household Name That Fills Fifty Char';

const server = setupApiServer(...signedInApiHandlers(undefined, [HOME]));

let uid: string;
beforeEach(async () => {
  uid = signInTestUser().uid;
  mockFocus.mockClear();
  await setWindowWidth(390);
});

async function setWindowWidth(width: number) {
  const metrics = { width, height: 900, scale: 1, fontScale: 1 };
  await act(async () => {
    Dimensions.set({ window: metrics, screen: metrics });
  });
}

function label(name: string) {
  return `Switch household, current: ${name}`;
}

async function remember(householdId: string, forUid = uid) {
  await AsyncStorage.setItem(rememberedHouseholdKey(forUid), householdId);
}

/** Answers `GET /households` with `households`, counting the requests. */
function answerHouseholds(households: ReturnType<typeof testHousehold>[]) {
  const api = { gets: 0, households };
  server.use(
    http.get(apiUrl('/households'), () => {
      api.gets += 1;
      return HttpResponse.json({ households: api.households });
    }),
  );
  return api;
}

async function press(element: Parameters<typeof fireEvent.press>[0]) {
  await fireEvent.press(element);
}

async function escape(testID: string) {
  await fireEvent(screen.getByTestId(testID), 'accessibilityEscape');
}

describe('routing', () => {
  test('a user with no household goes to onboarding', async () => {
    answerHouseholds([]);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(await screen.findByRole('heading', { name: 'Set up your pantry' })).toBeTruthy();
    expect(screen).toHavePathname('/onboarding');
    expect(screen.getByRole('heading', { name: 'Create a household' })).toBeTruthy();
    expect(screen.getByRole('heading', { name: 'Join with an invite code' })).toBeTruthy();
  });

  test('a user with a household goes to Pantry, with a loading state and no onboarding flash first', async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    server.use(
      http.get(apiUrl('/households'), async () => {
        await gate;
        return HttpResponse.json({ households: [HOME] });
      }),
    );
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(screen.getByTestId('households-loading')).toBeTruthy();
    expect(screen.queryByText('Set up your pantry')).toBeNull();

    await act(async () => release());
    expect(await screen.findByLabelText(label('Home'))).toBeTruthy();
    expect(screen).toHavePathname('/');
    expect(screen.queryByText('Set up your pantry')).toBeNull();
  });

  test('a failed list shows Retry, not onboarding, and Retry loads Pantry', async () => {
    server.use(
      http.get(apiUrl('/households'), () =>
        HttpResponse.json({ error: { code: 'internal', message: 'boom' } }, { status: 500 }),
      ),
    );
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(await screen.findByRole('button', { name: 'Retry' })).toBeTruthy();
    expect(screen.queryByText('Set up your pantry')).toBeNull();
    expect(screen.getByText('The server ran into a problem. Please try again in a moment.')).toBeTruthy();

    answerHouseholds([HOME]);
    await press(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByLabelText(label('Home'))).toBeTruthy();
  });

  test('a user with households who opens /onboarding is sent to /', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/onboarding' });

    expect(await screen.findByLabelText(label('Home'))).toBeTruthy();
    expect(screen).toHavePathname('/');
  });
});

describe('onboarding: create', () => {
  function fakeCreate(respond?: () => Response | Promise<Response>) {
    const api = { bodies: [] as unknown[] };
    server.use(
      http.post(apiUrl('/households'), async ({ request }) => {
        const body = (await request.json()) as { name: string };
        api.bodies.push(body);
        if (respond) return respond();
        return HttpResponse.json(testHousehold({ id: HOME.id, name: body.name }), { status: 201 });
      }),
    );
    return api;
  }

  test('creates the household with a trimmed name and opens Pantry with it in the switcher', async () => {
    answerHouseholds([]);
    const api = fakeCreate();
    await renderRouter(APP_DIR, { initialUrl: '/' });

    const input = await screen.findByTestId('household-name-input');
    expect(input.props.placeholder).toBe('e.g. Home');
    await fireEvent.changeText(input, '  Home  ');
    await press(screen.getByRole('button', { name: 'Create household' }));

    expect(await screen.findByLabelText(label('Home'))).toBeTruthy();
    expect(screen).toHavePathname('/');
    expect(api.bodies).toEqual([{ name: 'Home' }]);
  });

  test('an empty or too-long name shows a message and sends nothing', async () => {
    answerHouseholds([]);
    const api = fakeCreate();
    await renderRouter(APP_DIR, { initialUrl: '/onboarding' });

    await fireEvent.changeText(await screen.findByTestId('household-name-input'), '   ');
    await press(screen.getByRole('button', { name: 'Create household' }));
    expect(screen.getByText('Enter a name for your household')).toBeTruthy();

    await fireEvent.changeText(screen.getByTestId('household-name-input'), 'x'.repeat(51));
    await press(screen.getByRole('button', { name: 'Create household' }));
    expect(screen.getByText('Use 50 characters or fewer')).toBeTruthy();

    expect(api.bodies).toEqual([]);
  });

  test('a failed create is not retried automatically; Try again sends it once more', async () => {
    answerHouseholds([]);
    let fail = true;
    const api = fakeCreate(() =>
      fail
        ? HttpResponse.json({ error: { code: 'internal', message: 'boom' } }, { status: 500 })
        : HttpResponse.json(testHousehold({ id: HOME.id, name: 'Home' }), { status: 201 }),
    );
    await renderRouter(APP_DIR, { initialUrl: '/onboarding' });

    await fireEvent.changeText(await screen.findByTestId('household-name-input'), 'Home');
    await press(screen.getByRole('button', { name: 'Create household' }));

    expect(await screen.findByTestId('create-household-error')).toBeTruthy();
    expect(api.bodies).toHaveLength(1);
    expect(screen).toHavePathname('/onboarding');

    fail = false;
    await press(screen.getByRole('button', { name: 'Try again' }));
    expect(await screen.findByLabelText(label('Home'))).toBeTruthy();
    expect(api.bodies).toHaveLength(2);
  });

  test('a double tap creates one household', async () => {
    answerHouseholds([]);
    const api = fakeCreate();
    await renderRouter(APP_DIR, { initialUrl: '/onboarding' });

    await fireEvent.changeText(await screen.findByTestId('household-name-input'), 'Home');
    const button = screen.getByRole('button', { name: 'Create household' });
    // The second tap lands while the first request is still pending.
    await press(button);
    await press(button);

    expect(await screen.findByLabelText(label('Home'))).toBeTruthy();
    expect(api.bodies).toHaveLength(1);
  });

  test('Sign out is reachable from onboarding', async () => {
    answerHouseholds([]);
    await renderRouter(APP_DIR, { initialUrl: '/onboarding' });

    await press(await screen.findByRole('button', { name: 'Sign out' }));
    await waitFor(() => expect(screen).toHavePathname('/sign-in'));
  });
});

describe('onboarding: join', () => {
  function fakeJoin(respond: () => Response) {
    const api = { codes: [] as string[] };
    server.use(
      http.post(apiUrl('/invites/join'), async ({ request }) => {
        api.codes.push(((await request.json()) as { code: string }).code);
        return respond();
      }),
    );
    return api;
  }

  test('joins with the code as typed (capitalised) and opens Pantry with "You joined <name>"', async () => {
    answerHouseholds([]);
    const api = fakeJoin(() => HttpResponse.json({ household: CABIN, alreadyMember: false }));
    await renderRouter(APP_DIR, { initialUrl: '/onboarding' });

    const input = await screen.findByTestId('invite-code-input');
    await fireEvent.changeText(input, 'k7mq 3x-pa');
    expect(screen.getByTestId('invite-code-input').props.value).toBe('K7MQ 3X-PA');
    await press(screen.getByRole('button', { name: 'Join household' }));

    expect(await screen.findByLabelText(label('Cabin'))).toBeTruthy();
    expect(screen.getByTestId('household-notice')).toHaveTextContent(/You joined Cabin/);
    expect(api.codes).toEqual(['K7MQ 3X-PA']);
  });

  test('alreadyMember makes it current with "You\'re already a member of <name>"', async () => {
    answerHouseholds([]);
    fakeJoin(() => HttpResponse.json({ household: CABIN, alreadyMember: true }));
    await renderRouter(APP_DIR, { initialUrl: '/onboarding' });

    await fireEvent.changeText(await screen.findByTestId('invite-code-input'), 'K7MQ3XPA');
    await press(screen.getByRole('button', { name: 'Join household' }));

    expect(await screen.findByLabelText(label('Cabin'))).toBeTruthy();
    expect(screen.getByTestId('household-notice')).toHaveTextContent(/You're already a member of Cabin/);
  });

  test.each([
    ['invite_not_found', 404, "That code doesn't match an invite. Check it and try again."],
    ['invite_expired', 410, 'This invite has expired. Ask for a new one.'],
    ['invite_revoked', 410, 'This invite is no longer valid. Ask for a new one.'],
  ])('%s keeps the code and shows its message', async (code, status, message) => {
    answerHouseholds([]);
    fakeJoin(() => HttpResponse.json({ error: { code, message: 'server text' } }, { status }));
    await renderRouter(APP_DIR, { initialUrl: '/onboarding' });

    await fireEvent.changeText(await screen.findByTestId('invite-code-input'), 'K7MQ-3XPA');
    await press(screen.getByRole('button', { name: 'Join household' }));

    expect(await screen.findByText(message)).toBeTruthy();
    expect(screen.getByTestId('invite-code-input').props.value).toBe('K7MQ-3XPA');
    expect(screen).toHavePathname('/onboarding');
  });
});

describe('current household', () => {
  test('the remembered household is restored', async () => {
    answerHouseholds([HOME, CABIN]);
    await remember(HOME.id);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(await screen.findByLabelText(label('Home'))).toBeTruthy();
  });

  test('a remembered household that is gone falls back to the first by name, without an error', async () => {
    answerHouseholds([HOME, CABIN]);
    await remember('0190f3a1-0000-7000-8000-0000000000ff');
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(await screen.findByLabelText(label('Cabin'))).toBeTruthy();
    expect(screen.queryByRole('alert')).toBeNull();
  });

  test('the remembered household is per user', async () => {
    answerHouseholds([HOME, CABIN]);
    await remember(HOME.id, 'someone-else');
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(await screen.findByLabelText(label('Cabin'))).toBeTruthy();
    expect(rememberedHouseholdKey(uid)).toContain(uid);
    expect(rememberedHouseholdKey(uid)).not.toBe(rememberedHouseholdKey('someone-else'));
  });

  test('a 404 for the current household reloads the list, switches, and says so', async () => {
    const api = answerHouseholds([HOME, CABIN]);
    await remember(HOME.id);
    await renderRouter(APP_DIR, { initialUrl: '/' });
    expect(await screen.findByLabelText(label('Home'))).toBeTruthy();

    // The user was removed from Home.
    api.households = [CABIN];
    const client = mockCreatedClients[mockCreatedClients.length - 1];
    await act(async () => {
      await client
        .fetchQuery({
          queryKey: queryKeys.households.pantry(HOME.id),
          queryFn: () => Promise.reject(new ApiError('http', 'HTTP 404: not_found', { status: 404, code: 'not_found' })),
          retry: false,
        })
        .catch(() => {});
    });

    expect(await screen.findByLabelText(label('Cabin'))).toBeTruthy();
    expect(screen.getByTestId('household-notice')).toHaveTextContent(/You no longer have access to Home/);
  });

  test('a 404 for something inside a household the user still belongs to changes nothing', async () => {
    answerHouseholds([HOME, CABIN]);
    await remember(HOME.id);
    await renderRouter(APP_DIR, { initialUrl: '/' });
    expect(await screen.findByLabelText(label('Home'))).toBeTruthy();

    const client = mockCreatedClients[mockCreatedClients.length - 1];
    await act(async () => {
      await client
        .fetchQuery({
          queryKey: [...queryKeys.households.pantry(HOME.id), 'gone-entry'],
          queryFn: () => Promise.reject(new ApiError('http', 'HTTP 404: not_found', { status: 404, code: 'not_found' })),
          retry: false,
        })
        .catch(() => {});
    });

    expect(screen.getByLabelText(label('Home'))).toBeTruthy();
    expect(screen.queryByTestId('household-notice')).toBeNull();
  });

  test('losing access to the only household goes to onboarding and says so', async () => {
    const api = answerHouseholds([HOME]);
    await renderRouter(APP_DIR, { initialUrl: '/' });
    expect(await screen.findByLabelText(label('Home'))).toBeTruthy();

    api.households = [];
    const client = mockCreatedClients[mockCreatedClients.length - 1];
    await act(async () => {
      await client
        .fetchQuery({
          queryKey: queryKeys.households.members(HOME.id),
          queryFn: () => Promise.reject(new ApiError('http', 'HTTP 404', { status: 404, code: 'not_found' })),
          retry: false,
        })
        .catch(() => {});
    });

    expect(await screen.findByRole('heading', { name: 'Set up your pantry' })).toBeTruthy();
    expect(screen.getByTestId('household-notice')).toHaveTextContent(/You no longer have access to Home/);
  });
});

describe('switcher placement', () => {
  test('below 768 px it is the Pantry header, not in the tab bar, and only on Pantry', async () => {
    answerHouseholds([HOME]);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    const button = await screen.findByLabelText(label('Home'));
    expect(within(screen.getByTestId('screen-content')).getByLabelText(label('Home'))).toBe(button);
    expect(within(screen.getByTestId('bottom-tab-bar')).queryByLabelText(label('Home'))).toBeNull();
    expect(screen.queryByRole('heading', { name: 'Pantry' })).toBeNull();

    await press(screen.getByRole('tab', { name: 'Settings' }));
    expect(screen.getByRole('heading', { name: 'Settings' })).toBeTruthy();
    expect(screen.queryByLabelText(label('Home'))).toBeNull();
  });

  test('at 768 px and wider it is in the side nav on every screen, and Pantry shows "Pantry"', async () => {
    answerHouseholds([HOME]);
    await setWindowWidth(768);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    const sideNav = await screen.findByTestId('side-nav');
    expect(within(sideNav).getByLabelText(label('Home'))).toBeTruthy();
    expect(within(screen.getByTestId('screen-content')).queryByLabelText(label('Home'))).toBeNull();
    expect(screen.getByRole('heading', { name: 'Pantry' })).toBeTruthy();
    expect(within(sideNav).getByTestId('household-tile', { includeHiddenElements: true })).toHaveTextContent('H');

    for (const tab of ['Settings']) {
      await press(within(sideNav).getByRole('tab', { name: tab }));
      expect(within(screen.getByTestId('side-nav')).getByLabelText(label('Home'))).toBeTruthy();
    }
  });

  test('resizing across 768 px keeps the current household and Pantry, and closes an open list', async () => {
    const api = answerHouseholds([HOME, CABIN]);
    await remember(HOME.id);
    await setWindowWidth(1024);
    await renderRouter(APP_DIR, { initialUrl: '/' });
    expect(await screen.findByLabelText(label('Home'))).toBeTruthy();
    const gets = api.gets;

    await press(screen.getByLabelText(label('Home')));
    expect(screen.getByTestId('household-popover')).toBeTruthy();

    await setWindowWidth(390);
    expect(screen.queryByTestId('household-popover')).toBeNull();
    expect(screen.queryByTestId('household-sheet')).toBeNull();
    expect(within(screen.getByTestId('screen-content')).getByLabelText(label('Home'))).toBeTruthy();
    expect(screen.queryByTestId('households-loading')).toBeNull();
    expect(screen.getByText("Your household's items will appear here.")).toBeTruthy();

    await setWindowWidth(800);
    expect(within(screen.getByTestId('side-nav')).getByLabelText(label('Home'))).toBeTruthy();
    expect(api.gets).toBe(gets);
  });

  test('a 50-character name is one truncated line in the header and side nav, and in full in the list', async () => {
    const long = testHousehold({ id: HOME.id, name: LONG_NAME });
    expect([...LONG_NAME]).toHaveLength(50);
    answerHouseholds([long]);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    const header = await screen.findByLabelText(label(LONG_NAME));
    const headerText = within(header).getByText(LONG_NAME);
    expect(headerText.props.numberOfLines).toBe(1);
    expect(headerText.props.ellipsizeMode).toBe('tail');

    await press(header);
    const row = screen.getByLabelText(`${LONG_NAME}, Owner, current`);
    expect(within(row).getByText(LONG_NAME).props.numberOfLines).toBeUndefined();
    await escape('household-sheet');

    await setWindowWidth(1024);
    const sideButton = within(screen.getByTestId('side-nav')).getByLabelText(label(LONG_NAME));
    expect(within(sideButton).getByText(LONG_NAME).props.numberOfLines).toBe(1);
  });
});

describe('switcher list', () => {
  test('below 768 px the header opens a "Your households" sheet; choosing a household switches and closes it', async () => {
    answerHouseholds([HOME, CABIN]);
    await remember(HOME.id);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    const button = await screen.findByLabelText(label('Home'));
    expect(button).toBeCollapsed();
    expect(button.props.role).toBe('button');
    await press(button);

    const sheet = screen.getByTestId('household-sheet');
    expect(sheet.props['aria-label']).toBe('Your households');
    expect(screen.getByLabelText(label('Home'))).toBeExpanded();
    const rows = within(sheet).getAllByLabelText(/, (Owner|Member)/);
    expect(rows.map((row) => row.props.accessibilityLabel ?? row.props['aria-label'])).toEqual(['Cabin, Member', 'Home, Owner, current']);
    expect(within(rows[1]).getByTestId('household-current-check', { includeHiddenElements: true })).toBeTruthy();
    expect(within(sheet).getByRole('button', { name: 'Create a household' })).toBeTruthy();
    expect(within(sheet).getByRole('button', { name: 'Join with a code' })).toBeTruthy();

    await press(rows[0]);
    expect(screen.queryByTestId('household-sheet')).toBeNull();
    expect(screen.getByLabelText(label('Cabin'))).toBeTruthy();
    expect(await AsyncStorage.getItem(rememberedHouseholdKey(uid))).toBe(CABIN.id);
  });

  test('the switcher opens with one household, and choosing the current one just closes the list', async () => {
    answerHouseholds([HOME]);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    await press(await screen.findByLabelText(label('Home')));
    await press(screen.getByLabelText('Home, Owner, current'));
    expect(screen.queryByTestId('household-sheet')).toBeNull();
    expect(screen.getByLabelText(label('Home'))).toBeTruthy();
  });

  test('two households with the same name are told apart by the check mark and switched by ID', async () => {
    const other = testHousehold({ id: CABIN.id, name: 'Home', role: 'member' });
    answerHouseholds([HOME, other]);
    await remember(HOME.id);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    await press(await screen.findByLabelText(label('Home')));
    await press(screen.getByLabelText('Home, Member'));
    expect(await AsyncStorage.getItem(rememberedHouseholdKey(uid))).toBe(other.id);

    await press(screen.getByLabelText(label('Home')));
    expect(screen.getByLabelText('Home, Member, current')).toBeTruthy();
    expect(screen.getByLabelText('Home, Owner')).toBeTruthy();
  });

  test('create from the sheet makes the new household current', async () => {
    answerHouseholds([HOME]);
    server.use(http.post(apiUrl('/households'), () => HttpResponse.json(CABIN, { status: 201 })));
    await renderRouter(APP_DIR, { initialUrl: '/' });

    await press(await screen.findByLabelText(label('Home')));
    await press(screen.getByRole('button', { name: 'Create a household' }));
    const sheet = screen.getByTestId('household-sheet');
    await fireEvent.changeText(within(sheet).getByTestId('household-name-input'), 'Cabin');
    await press(within(sheet).getByRole('button', { name: 'Create household' }));

    expect(await screen.findByLabelText(label('Cabin'))).toBeTruthy();
    expect(screen.queryByTestId('household-sheet')).toBeNull();
  });
});

describe('side-nav switcher', () => {
  beforeEach(async () => {
    await setWindowWidth(1024);
  });

  test('has its label and aria-expanded, opens a popover, and Escape closes it and returns focus', async () => {
    answerHouseholds([HOME, CABIN]);
    await remember(HOME.id);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    const button = await screen.findByLabelText(label('Home'));
    expect(button.props.role).toBe('button');
    expect(button).toBeCollapsed();

    await press(button);
    const popover = screen.getByTestId('household-popover');
    expect(popover.props['aria-label']).toBe('Your households');
    expect(screen.queryByTestId('household-sheet')).toBeNull();
    expect(screen.getByLabelText(label('Home'))).toBeExpanded();
    // Focus moved to the current household's row.
    expect(mockFocus).toHaveBeenCalled();
    mockFocus.mockClear();

    // Escape on web, the back button on Android and the VoiceOver escape gesture all close it.
    await escape('household-popover');
    expect(screen.queryByTestId('household-popover')).toBeNull();
    expect(screen.getByLabelText(label('Home'))).toBeCollapsed();
    expect(screen.getByLabelText(label('Home'))).toBeTruthy();
    // The last focus call is the switcher button (the row's ref is also called with null as it unmounts).
    expect(mockFocus).toHaveBeenLastCalledWith(expect.anything());
  });

  test('clicking outside closes the popover without switching', async () => {
    answerHouseholds([HOME, CABIN]);
    await remember(HOME.id);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    await press(await screen.findByLabelText(label('Home')));
    mockFocus.mockClear();
    // The backdrop covers the whole window outside the popover.
    await fireEvent(screen.getAllByTestId('overlay-backdrop', { includeHiddenElements: true })[0], 'responderRelease');

    expect(screen.queryByTestId('household-popover')).toBeNull();
    expect(screen.getByLabelText(label('Home'))).toBeTruthy();
    expect(mockFocus).toHaveBeenCalled();
  });

  test('switching on Settings stays on Settings with the new name in the side nav', async () => {
    answerHouseholds([HOME, CABIN]);
    await remember(HOME.id);
    await renderRouter(APP_DIR, { initialUrl: '/settings' });

    await press(await screen.findByLabelText(label('Home')));
    await press(screen.getByLabelText('Cabin, Member'));

    expect(screen).toHavePathname('/settings');
    expect(within(screen.getByTestId('side-nav')).getByLabelText(label('Cabin'))).toBeTruthy();
  });

  test.each(['/add', '/entries/abc'])('switching on %s goes to Pantry for the new household', async (url) => {
    answerHouseholds([HOME, CABIN]);
    await remember(HOME.id);
    await renderRouter(APP_DIR, { initialUrl: url });

    await press(await screen.findByLabelText(label('Home')));
    await press(screen.getByLabelText('Cabin, Member'));

    await waitFor(() => expect(screen).toHavePathname('/'));
    expect(screen.getByRole('heading', { name: 'Pantry' })).toBeTruthy();
    expect(screen.getByLabelText(label('Cabin'))).toBeTruthy();
  });

  test('Join with a code closes the popover and opens a centred dialog; joining makes it current', async () => {
    answerHouseholds([HOME]);
    server.use(
      http.post(apiUrl('/invites/join'), () => HttpResponse.json({ household: CABIN, alreadyMember: false })),
    );
    await renderRouter(APP_DIR, { initialUrl: '/' });

    await press(await screen.findByLabelText(label('Home')));
    await press(screen.getByRole('button', { name: 'Join with a code' }));
    expect(screen.queryByTestId('household-popover')).toBeNull();
    const dialog = screen.getByTestId('household-dialog');
    expect(dialog.props['aria-label']).toBe('Join with a code');

    await fireEvent.changeText(within(dialog).getByTestId('invite-code-input'), 'abcd-efgh');
    await press(within(dialog).getByRole('button', { name: 'Join household' }));

    expect(await screen.findByLabelText(label('Cabin'))).toBeTruthy();
    expect(screen.queryByTestId('household-dialog')).toBeNull();
    expect(screen.getByTestId('household-notice')).toHaveTextContent(/You joined Cabin/);
  });

  test('Escape closes the create dialog without creating anything', async () => {
    answerHouseholds([HOME]);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    await press(await screen.findByLabelText(label('Home')));
    await press(screen.getByRole('button', { name: 'Create a household' }));
    mockFocus.mockClear();
    await escape('household-dialog');

    expect(screen.queryByTestId('household-dialog')).toBeNull();
    expect(screen.getByLabelText(label('Home'))).toBeTruthy();
    expect(mockFocus).toHaveBeenCalled();
  });
});

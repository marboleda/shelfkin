// The invite link route, /join/<code> (#33): signed-in joins, errors, and the
// signed-out flow that keeps the code on the device until after sign-in.

import AsyncStorage from '@react-native-async-storage/async-storage';
import { act, fireEvent, waitFor } from '@testing-library/react-native';
import { router } from 'expo-router';
import { renderRouter, screen, testRouter } from 'expo-router/testing-library';
import { http, HttpResponse } from 'msw';
import { StrictMode } from 'react';

import { signOut } from '@/auth/session';
import {
  PENDING_INVITE_KEY,
  PENDING_INVITE_MESSAGE,
  resetPendingInviteForTests,
} from '@/household/pending-invite';
import { apiUrl, setupApiServer } from '../test-utils/api';
import {
  mockFirebase,
  signedInApiHandlers,
  signInTestUser,
  TEST_EMAIL,
  TEST_PASSWORD,
  testHousehold,
} from '../test-utils/auth';

jest.setTimeout(120000);

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

jest.mock('@/auth/timezone', () => ({ deviceTimeZone: () => 'America/Toronto' }));

jest.mock('@/api/query-client', () => {
  const actual = jest.requireActual('@/api/query-client');
  return { ...actual, createQueryClient: () => actual.createQueryClient({ retryDelay: 0, gcTime: Infinity }) };
});

const APP_DIR = './src/app';

const HOME = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000a', name: 'Home', role: 'owner' });
const CABIN = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000b', name: 'The Cabin', role: 'member' });

const server = setupApiServer(...signedInApiHandlers(undefined, [HOME]));

function switcherLabel(name: string) {
  return `Switch household, current: ${name}`;
}

function answerHouseholds(households: ReturnType<typeof testHousehold>[]) {
  server.use(http.get(apiUrl('/households'), () => HttpResponse.json({ households })));
}

type JoinReply = { status: number; body: unknown } | 'network';

/** Answers `POST /invites/join`, recording each code sent. Replies are used in order; the last one repeats. */
function fakeJoin(...replies: JoinReply[]) {
  const api = { codes: [] as string[] };
  server.use(
    http.post(apiUrl('/invites/join'), async ({ request }) => {
      api.codes.push(((await request.json()) as { code: string }).code);
      const reply = replies[Math.min(api.codes.length - 1, replies.length - 1)] ?? joined(CABIN);
      if (reply === 'network') return HttpResponse.error();
      return HttpResponse.json(reply.body as object, { status: reply.status });
    }),
  );
  return api;
}

function joined(household = CABIN, alreadyMember = false): JoinReply {
  return { status: 200, body: { household, alreadyMember } };
}

function apiError(status: number, code: string): JoinReply {
  return { status, body: { error: { code, message: code } } };
}

async function signInWithForm() {
  await fireEvent.changeText(screen.getByTestId('email-input'), TEST_EMAIL);
  await fireEvent.changeText(screen.getByTestId('password-input'), TEST_PASSWORD);
  await act(async () => {
    fireEvent.press(screen.getByRole('button', { name: 'Sign in' }));
  });
}

async function storedCode() {
  return AsyncStorage.getItem(PENDING_INVITE_KEY);
}

describe('signed in', () => {
  beforeEach(() => {
    signInTestUser();
  });

  test('shows "Joining…", joins once, makes the household current and replaces the screen with Pantry', async () => {
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    const api = { codes: [] as string[] };
    server.use(
      http.post(apiUrl('/invites/join'), async ({ request }) => {
        api.codes.push(((await request.json()) as { code: string }).code);
        await gate;
        return HttpResponse.json({ household: CABIN, alreadyMember: false });
      }),
    );
    await renderRouter(APP_DIR, { initialUrl: '/join/K7QM4XPA' });

    expect(await screen.findByRole('heading', { name: 'Joining…' })).toBeTruthy();
    await act(async () => release());

    expect(await screen.findByLabelText(switcherLabel('The Cabin'))).toBeTruthy();
    expect(screen).toHavePathname('/');
    expect(screen.getByTestId('household-notice')).toHaveTextContent(/You joined The Cabin/);
    expect(api.codes).toEqual(['K7QM4XPA']);
    // The join screen was replaced: nothing to go back to.
    expect(testRouter.canGoBack()).toBe(false);
  });

  test('already a member: opens that household with "You\'re already a member of <name>"', async () => {
    answerHouseholds([HOME, CABIN]);
    fakeJoin(joined(CABIN, true));
    await renderRouter(APP_DIR, { initialUrl: '/join/K7QM4XPA' });

    expect(await screen.findByLabelText(switcherLabel('The Cabin'))).toBeTruthy();
    expect(screen.getByTestId('household-notice')).toHaveTextContent(/You're already a member of The Cabin/);
  });

  test('a user with no household joins straight into Pantry, without onboarding', async () => {
    answerHouseholds([]);
    fakeJoin(joined(CABIN));
    await renderRouter(APP_DIR, { initialUrl: '/join/K7QM4XPA' });

    expect(await screen.findByLabelText(switcherLabel('The Cabin'))).toBeTruthy();
    expect(screen).toHavePathname('/');
    expect(screen.queryByText('Set up your pantry')).toBeNull();
  });

  test('sends the request once, also under Strict Mode and on re-render', async () => {
    const api = fakeJoin(joined(CABIN));
    const result = await renderRouter(APP_DIR, { initialUrl: '/join/k7qm-4xpa', wrapper: StrictMode });

    expect(await screen.findByLabelText(switcherLabel('The Cabin'))).toBeTruthy();
    result.rerender(<></>);
    // Lower case and a dash are sent as they are; the API normalises them.
    expect(api.codes).toEqual(['k7qm-4xpa']);
  });

  test.each([
    ['invite_not_found', 404, "That code doesn't match an invite. Check it and try again."],
    ['invite_expired', 410, 'This invite has expired. Ask for a new one.'],
    ['invite_revoked', 410, 'This invite is no longer valid. Ask for a new one.'],
  ])('%s shows the #26 message and "Go to Pantry" for a user with households', async (code, status, message) => {
    const api = fakeJoin(apiError(status, code));
    await renderRouter(APP_DIR, { initialUrl: '/join/K7QM4XPA' });

    expect(await screen.findByText(message)).toBeTruthy();
    expect(screen.getByRole('heading', { name: "Couldn't join the household" })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Retry' })).toBeNull();

    await fireEvent.press(screen.getByRole('button', { name: 'Go to Pantry' }));
    expect(await screen.findByLabelText(switcherLabel('Home'))).toBeTruthy();
    expect(api.codes).toHaveLength(1);
  });

  test('an expired code offers "Create or join a household" to a user with no household', async () => {
    answerHouseholds([]);
    fakeJoin(apiError(410, 'invite_expired'));
    await renderRouter(APP_DIR, { initialUrl: '/join/K7QM4XPA' });

    expect(await screen.findByText('This invite has expired. Ask for a new one.')).toBeTruthy();
    await fireEvent.press(await screen.findByRole('button', { name: 'Create or join a household' }));
    expect(await screen.findByRole('heading', { name: 'Set up your pantry' })).toBeTruthy();
  });

  test('a code that matched nothing can be corrected in the shared join form', async () => {
    const api = fakeJoin(apiError(404, 'invite_not_found'), joined(CABIN));
    await renderRouter(APP_DIR, { initialUrl: '/join/K7QM4XPB' });

    const input = await screen.findByTestId('invite-code-input');
    expect(input.props.value).toBe('K7QM4XPB');
    await fireEvent.changeText(input, 'K7QM4XPA');
    await fireEvent.press(screen.getByRole('button', { name: 'Join household' }));

    expect(await screen.findByLabelText(switcherLabel('The Cabin'))).toBeTruthy();
    expect(api.codes).toEqual(['K7QM4XPB', 'K7QM4XPA']);
  });

  test('a network error shows the #24 message with Retry, which joins', async () => {
    const api = fakeJoin('network', joined(CABIN));
    await renderRouter(APP_DIR, { initialUrl: '/join/K7QM4XPA' });

    expect(await screen.findByText("Can't reach the server. Check your connection and try again.")).toBeTruthy();
    await fireEvent.press(screen.getByRole('button', { name: 'Retry' }));

    expect(await screen.findByLabelText(switcherLabel('The Cabin'))).toBeTruthy();
    expect(api.codes).toEqual(['K7QM4XPA', 'K7QM4XPA']);
  });

  test('/join/ with no code says the link is incomplete, with a link to Pantry', async () => {
    const api = fakeJoin();
    await renderRouter(APP_DIR, { initialUrl: '/join/' });

    expect(await screen.findByRole('heading', { name: 'This invite link is incomplete' })).toBeTruthy();
    await fireEvent.press(screen.getByRole('link', { name: 'Go to Pantry' }));
    expect(await screen.findByLabelText(switcherLabel('Home'))).toBeTruthy();
    expect(api.codes).toEqual([]);
  });
});

describe('signed out', () => {
  test('goes to /sign-in with the invite message, keeps the code, and joins after signing in', async () => {
    mockFirebase.addAccount(TEST_EMAIL, TEST_PASSWORD);
    const api = fakeJoin(joined(CABIN));
    await renderRouter(APP_DIR, { initialUrl: '/join/K7QM4XPA' });

    await waitFor(() => expect(screen).toHavePathname('/sign-in'));
    expect(await screen.findByText(PENDING_INVITE_MESSAGE)).toBeTruthy();
    expect(await storedCode()).toBe('K7QM4XPA');
    expect(api.codes).toEqual([]);

    await signInWithForm();

    expect(await screen.findByLabelText(switcherLabel('The Cabin'))).toBeTruthy();
    expect(screen.getByTestId('household-notice')).toHaveTextContent(/You joined The Cabin/);
    expect(api.codes).toEqual(['K7QM4XPA']);
    expect(await storedCode()).toBeNull();
    expect(testRouter.canGoBack()).toBe(false);
  });

  test('the code survives switching to sign-up, and signing up joins without onboarding', async () => {
    answerHouseholds([]);
    const api = fakeJoin(joined(CABIN));
    await renderRouter(APP_DIR, { initialUrl: '/join/K7QM4XPA' });

    await waitFor(() => expect(screen).toHavePathname('/sign-in'));
    await fireEvent.press(await screen.findByRole('link', { name: 'Create an account' }));
    await waitFor(() => expect(screen).toHavePathname('/sign-up'));
    expect(screen.getByText(PENDING_INVITE_MESSAGE)).toBeTruthy();

    await fireEvent.changeText(screen.getByTestId('name-input'), 'Sam');
    await fireEvent.changeText(screen.getByTestId('email-input'), TEST_EMAIL);
    await fireEvent.changeText(screen.getByTestId('password-input'), TEST_PASSWORD);
    await act(async () => {
      fireEvent.press(screen.getByRole('button', { name: 'Create account' }));
    });

    expect(await screen.findByLabelText(switcherLabel('The Cabin'))).toBeTruthy();
    expect(screen.queryByText('Set up your pantry')).toBeNull();
    expect(api.codes).toEqual(['K7QM4XPA']);
  });

  test('the code is read back from storage after a restart (remount) and joins after signing in', async () => {
    mockFirebase.addAccount(TEST_EMAIL, TEST_PASSWORD);
    await AsyncStorage.setItem(PENDING_INVITE_KEY, 'K7QM4XPA');
    resetPendingInviteForTests();
    const api = fakeJoin(joined(CABIN));
    await renderRouter(APP_DIR, { initialUrl: '/sign-in' });

    expect(await screen.findByText(PENDING_INVITE_MESSAGE)).toBeTruthy();
    await signInWithForm();

    expect(await screen.findByLabelText(switcherLabel('The Cabin'))).toBeTruthy();
    expect(api.codes).toEqual(['K7QM4XPA']);
  });

  test('a second invite link replaces the first pending code', async () => {
    mockFirebase.addAccount(TEST_EMAIL, TEST_PASSWORD);
    const api = fakeJoin(joined(CABIN));
    await renderRouter(APP_DIR, { initialUrl: '/join/FIRST111' });
    await waitFor(() => expect(screen).toHavePathname('/sign-in'));

    await act(async () => {
      router.navigate('/join/SECOND22');
    });
    await waitFor(() => expect(screen).toHavePathname('/sign-in'));
    expect(await storedCode()).toBe('SECOND22');

    await signInWithForm();
    expect(await screen.findByLabelText(switcherLabel('The Cabin'))).toBeTruthy();
    expect(api.codes).toEqual(['SECOND22']);
  });

  test('a definite error clears the code; a network error keeps it', async () => {
    mockFirebase.addAccount(TEST_EMAIL, TEST_PASSWORD);
    fakeJoin('network', apiError(410, 'invite_expired'));
    await renderRouter(APP_DIR, { initialUrl: '/join/K7QM4XPA' });
    await waitFor(() => expect(screen).toHavePathname('/sign-in'));
    await signInWithForm();

    expect(await screen.findByRole('button', { name: 'Retry' })).toBeTruthy();
    expect(await storedCode()).toBe('K7QM4XPA');

    await fireEvent.press(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText('This invite has expired. Ask for a new one.')).toBeTruthy();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Go to Pantry' })).toBeTruthy());
    expect(await storedCode()).toBeNull();
  });

  test('signing out clears the code, so a later sign-in does not join', async () => {
    mockFirebase.addAccount(TEST_EMAIL, TEST_PASSWORD);
    // Signed in first, so the sign-out below is a real one.
    signInTestUser();
    const api = fakeJoin(joined(CABIN));
    await renderRouter(APP_DIR, { initialUrl: '/' });
    expect(await screen.findByLabelText(switcherLabel('Home'))).toBeTruthy();

    // A code left on the device, e.g. from a link opened in another tab.
    await AsyncStorage.setItem(PENDING_INVITE_KEY, 'K7QM4XPA');
    resetPendingInviteForTests();
    await act(async () => {
      await signOut();
    });
    await waitFor(() => expect(screen).toHavePathname('/sign-in'));
    expect(await storedCode()).toBeNull();
    expect(screen.queryByText(PENDING_INVITE_MESSAGE)).toBeNull();

    await signInWithForm();
    expect(await screen.findByLabelText(switcherLabel('Home'))).toBeTruthy();
    expect(api.codes).toEqual([]);
  });
});

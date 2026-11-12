import AsyncStorage from '@react-native-async-storage/async-storage';
import { act, cleanup, fireEvent, waitFor } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { createUserWithEmailAndPassword } from 'firebase/auth';
import { http, HttpResponse } from 'msw';

import { apiUrl, setupApiServer } from '../test-utils/api';
import { mockFirebase, signedInApiHandlers, TEST_EMAIL, TEST_PASSWORD, testMe } from '../test-utils/auth';

jest.setTimeout(60000);

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

jest.mock('@/auth/timezone', () => ({ deviceTimeZone: () => 'Europe/Paris' }));

const APP_DIR = './src/app';
const server = setupApiServer(...signedInApiHandlers());
const mockCreate = createUserWithEmailAndPassword as jest.MockedFunction<typeof createUserWithEmailAndPassword>;

/** Records each PATCH /me body; the first `failures` requests get a 500. */
function recordPatches(failures = 0) {
  const bodies: unknown[] = [];
  server.use(
    http.patch(apiUrl('/me'), async ({ request }) => {
      const body = (await request.json()) as Record<string, string>;
      bodies.push(body);
      if (bodies.length <= failures) {
        return HttpResponse.json({ error: { code: 'internal', message: 'Something went wrong.' } }, { status: 500 });
      }
      return HttpResponse.json(testMe({ ...body, email: 'alex@example.com' }));
    }),
  );
  return bodies;
}

beforeEach(() => {
  mockCreate.mockClear();
});

/**
 * The name kept on the device after sign-up, read synchronously from the
 * AsyncStorage mock (an async read inside waitFor would overlap act() calls).
 */
function pendingProfile(): unknown {
  const storage = (AsyncStorage as unknown as { __INTERNAL_MOCK_STORAGE__: Record<string, string | undefined> })
    .__INTERNAL_MOCK_STORAGE__;
  const raw = storage['shelfkin.pendingProfile'];
  return raw ? JSON.parse(raw) : null;
}

async function openSignUp() {
  await renderRouter(APP_DIR, { initialUrl: '/sign-up' });
  expect(screen.getByRole('heading', { name: 'Create account' })).toBeTruthy();
}

async function fill(name: string, email: string, password: string) {
  await fireEvent.changeText(screen.getByTestId('name-input'), name);
  await fireEvent.changeText(screen.getByTestId('email-input'), email);
  await fireEvent.changeText(screen.getByTestId('password-input'), password);
}

async function submit() {
  await act(async () => {
    await fireEvent.press(screen.getByRole('button', { name: 'Create account' }));
  });
}

describe('validation', () => {
  test('each invalid field shows its own message and nothing is sent', async () => {
    await openSignUp();

    await fill('   ', 'not-an-email', 'short');
    await submit();

    expect(screen.getByText('Enter your name')).toBeTruthy();
    expect(screen.getByText('Enter a valid email address, like you@example.com')).toBeTruthy();
    expect(screen.getByText('Use at least 8 characters')).toBeTruthy();
    expect(mockCreate).not.toHaveBeenCalled();
  });

  test('a name over 50 characters is rejected', async () => {
    await openSignUp();

    await fill('a'.repeat(51), 'alex@example.com', TEST_PASSWORD);
    await submit();

    expect(screen.getByText('Use 50 characters or fewer')).toBeTruthy();
    expect(mockCreate).not.toHaveBeenCalled();
  });

  test('the fields support autofill', async () => {
    await openSignUp();

    expect(screen.getByTestId('name-input')).toHaveProp('autoComplete', 'name');
    expect(screen.getByTestId('email-input')).toHaveProp('autoComplete', 'email');
    expect(screen.getByTestId('password-input')).toHaveProp('autoComplete', 'new-password');
    expect(screen.getByTestId('password-input')).toHaveProp('textContentType', 'newPassword');
    expect(screen.getByTestId('password-input')).toHaveProp('secureTextEntry', true);
  });
});

describe('success', () => {
  test('signs in, sends PATCH /me with the trimmed name and time zone, and moves on to /', async () => {
    const patches = recordPatches();
    await openSignUp();

    await fill('  Alex  ', 'alex@example.com', TEST_PASSWORD);
    await submit();

    expect(mockCreate).toHaveBeenCalledWith(expect.anything(), 'alex@example.com', TEST_PASSWORD);
    await waitFor(() => expect(screen).toHavePathname('/'));
    await waitFor(() => expect(patches).toEqual([{ displayName: 'Alex', timezone: 'Europe/Paris' }]));
    await waitFor(() => expect(pendingProfile()).toBeNull());
  });

  test('if PATCH /me fails, the user stays signed in and the name is sent again on the next start', async () => {
    const warn = jest.spyOn(console, 'warn').mockImplementation(() => {});
    const patches = recordPatches(1);
    await openSignUp();

    await fill('Alex', 'alex@example.com', TEST_PASSWORD);
    await submit();

    await waitFor(() => expect(patches).toHaveLength(1));
    await waitFor(() => expect(screen).toHavePathname('/'));
    expect(mockFirebase.currentUser?.email).toBe('alex@example.com');
    expect(pendingProfile()).toEqual({
      email: 'alex@example.com',
      displayName: 'Alex',
    });

    // Close and reopen the app: the session is restored and the name is sent again.
    await cleanup();
    await renderRouter(APP_DIR, { initialUrl: '/' });

    await waitFor(() => expect(patches).toHaveLength(2));
    expect(patches[1]).toEqual({ displayName: 'Alex', timezone: 'Europe/Paris' });
    await waitFor(() => expect(pendingProfile()).toBeNull());
    warn.mockRestore();
  });
});

describe('email already in use', () => {
  test('shows the message with a link to sign in, and keeps the typed email', async () => {
    mockFirebase.addAccount('alex@example.com', 'other-password');
    await openSignUp();

    await fill('Alex', 'alex@example.com', TEST_PASSWORD);
    await submit();

    expect(screen.getByTestId('form-error')).toHaveTextContent(/An account with this email already exists/);
    expect(screen.getByTestId('email-input')).toHaveProp('value', 'alex@example.com');
    expect(pendingProfile()).toBeNull();

    await act(async () => {
      await fireEvent.press(screen.getByText('Sign in instead'));
    });
    expect(screen).toHavePathname('/sign-in');
    expect(screen.getByTestId('email-input')).toHaveProp('value', 'alex@example.com');
  });

  test('a network error shows a readable message', async () => {
    mockFirebase.failNext('auth/network-request-failed');
    await openSignUp();

    await fill('Alex', TEST_EMAIL, TEST_PASSWORD);
    await submit();

    expect(screen.getByTestId('form-error')).toHaveTextContent(/Can't connect/);
    expect(screen.queryByText(/auth\//)).toBeNull();
  });
});

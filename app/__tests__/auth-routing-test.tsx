import type { QueryClient } from '@tanstack/react-query';
import { act, fireEvent, waitFor } from '@testing-library/react-native';
import { renderRouter, screen, testRouter } from 'expo-router/testing-library';
import { http, HttpResponse } from 'msw';

import { addBeforeSignOutTask } from '@/auth/session';
import { apiUrl, setupApiServer } from '../test-utils/api';
import { mockFirebase, signedInApiHandlers, signInTestUser, TEST_EMAIL, TEST_PASSWORD } from '../test-utils/auth';

jest.setTimeout(60000);

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

jest.mock('@/auth/timezone', () => ({ deviceTimeZone: () => 'America/Toronto' }));

// Keep each QueryClient the root layout creates, to check what sign-out leaves in it.
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
const server = setupApiServer(...signedInApiHandlers());

async function signInWithForm(email = TEST_EMAIL, password = TEST_PASSWORD) {
  await fireEvent.changeText(screen.getByTestId('email-input'), email);
  await fireEvent.changeText(screen.getByTestId('password-input'), password);
  await act(async () => {
    fireEvent.press(screen.getByRole('button', { name: 'Sign in' }));
  });
}

describe('signed out', () => {
  test.each(['/', '/add', '/settings', '/onboarding', '/entries/abc'])('%s lands on /sign-in', async (url) => {
    await renderRouter(APP_DIR, { initialUrl: url });

    expect(screen).toHavePathname('/sign-in');
    expect(screen.getByRole('heading', { name: 'Sign in' })).toBeTruthy();
    expect(screen.queryByTestId('bottom-tab-bar')).toBeNull();
  });

  test('a visit to /entries/abc redirects to /sign-in, and signing in returns to /entries/abc', async () => {
    mockFirebase.addAccount(TEST_EMAIL, TEST_PASSWORD);
    await renderRouter(APP_DIR, { initialUrl: '/entries/abc' });

    expect(screen).toHavePathname('/sign-in');
    expect(screen).toHaveSearchParams({ redirect: '/entries/abc' });

    await signInWithForm();

    await waitFor(() => expect(screen).toHavePathname('/entries/abc'));
    expect(screen.getByTestId('entry-id')).toHaveTextContent('abc');
  });

  test('signing in from a plain /sign-in goes to Pantry', async () => {
    mockFirebase.addAccount(TEST_EMAIL, TEST_PASSWORD);
    await renderRouter(APP_DIR, { initialUrl: '/sign-in' });

    await signInWithForm();

    await waitFor(() => expect(screen).toHavePathname('/'));
    expect(await screen.findByRole('heading', { name: 'Pantry' })).toBeTruthy();
    // Back does not return to the sign-in screen.
    expect(testRouter.canGoBack()).toBe(false);
  });

  test('/sign-in links to /sign-up and back', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/sign-in' });

    await act(async () => {
      fireEvent.press(screen.getByText('Create an account'));
    });
    expect(screen).toHavePathname('/sign-up');
    expect(screen.getByRole('heading', { name: 'Create account' })).toBeTruthy();

    await act(async () => {
      fireEvent.press(screen.getByText('Sign in'));
    });
    expect(screen).toHavePathname('/sign-in');
  });
});

describe('signed in', () => {
  beforeEach(() => {
    signInTestUser();
  });

  test.each(['/sign-in', '/sign-up', '/sign-in?redirect=//evil.example'])('%s sends the user to /', async (url) => {
    await renderRouter(APP_DIR, { initialUrl: url });

    await waitFor(() => expect(screen).toHavePathname('/'));
    expect(await screen.findByRole('heading', { name: 'Pantry' })).toBeTruthy();
  });

  test('Sign out runs the before-sign-out tasks, clears the query cache and lands on /sign-in', async () => {
    const order: string[] = [];
    addBeforeSignOutTask(async () => {
      order.push(`task while signed in: ${mockFirebase.currentUser !== null}`);
    });
    await renderRouter(APP_DIR, { initialUrl: '/settings' });
    await waitFor(() => expect(screen.getByTestId('account-name-value')).toHaveTextContent('Sam'));
    const client = mockCreatedClients[mockCreatedClients.length - 1];
    expect(client.getQueryCache().getAll().length).toBeGreaterThan(0);

    await act(async () => {
      fireEvent.press(screen.getByRole('button', { name: 'Sign out' }));
    });

    await waitFor(() => expect(screen).toHavePathname('/sign-in'));
    expect(order).toEqual(['task while signed in: true']);
    expect(mockFirebase.currentUser).toBeNull();
    expect(client.getQueryCache().getAll()).toEqual([]);
    // No redirect back to Settings after the next sign-in, and no way back to it.
    expect(screen).toHaveSearchParams({});
    expect(testRouter.canGoBack()).toBe(false);
  });

  test('a failing before-sign-out task does not stop the sign-out', async () => {
    const warn = jest.spyOn(console, 'warn').mockImplementation(() => {});
    addBeforeSignOutTask(() => Promise.reject(new Error('offline')));
    await renderRouter(APP_DIR, { initialUrl: '/settings' });

    await act(async () => {
      fireEvent.press(screen.getByRole('button', { name: 'Sign out' }));
    });

    await waitFor(() => expect(screen).toHavePathname('/sign-in'));
    warn.mockRestore();
  });

  test('signing out in another tab moves this one to /sign-in', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/settings' });
    expect(screen.getByRole('heading', { name: 'Settings' })).toBeTruthy();

    await act(async () => {
      mockFirebase.signOutElsewhere();
    });

    await waitFor(() => expect(screen).toHavePathname('/sign-in'));
  });

  test('when the API rejects the refreshed token too, the user is signed out and told the session ended', async () => {
    server.use(
      http.get(apiUrl('/me'), () =>
        HttpResponse.json({ error: { code: 'unauthorized', message: 'nope' } }, { status: 401 }),
      ),
    );
    await renderRouter(APP_DIR, { initialUrl: '/settings' });

    await waitFor(() => expect(screen).toHavePathname('/sign-in'));
    expect(screen.getByTestId('session-ended')).toHaveTextContent('Your session has ended. Please sign in again.');
  });
});

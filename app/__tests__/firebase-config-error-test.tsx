import { renderRouter, screen } from 'expo-router/testing-library';
import { initializeAuth } from 'firebase/auth';

jest.setTimeout(60000);

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

// A production build with its API URL but pointed at the Auth emulator.
jest.mock('@/auth/config', () => {
  const actual = jest.requireActual('@/auth/config');
  return {
    ...actual,
    firebaseConfig: actual.resolveFirebaseConfig(
      { apiKey: 'key', authDomain: 'shelfkin.firebaseapp.com', projectId: 'shelfkin', emulatorHost: 'localhost:9099' },
      false,
    ),
  };
});

const fetchSpy = jest.fn();

beforeEach(() => {
  fetchSpy.mockReset();
  globalThis.fetch = fetchSpy;
});

test('a production build with bad Firebase settings shows a configuration error and never starts Firebase', async () => {
  await renderRouter('./src/app', { initialUrl: '/settings' });

  expect(screen.getByRole('heading', { name: 'App not configured' })).toBeTruthy();
  expect(screen.getByTestId('config-error')).toHaveTextContent(/EXPO_PUBLIC_FIREBASE_AUTH_EMULATOR_HOST/);
  expect(screen.queryByRole('heading', { name: 'Sign in' })).toBeNull();
  expect(initializeAuth).not.toHaveBeenCalled();
  expect(fetchSpy).not.toHaveBeenCalled();
});

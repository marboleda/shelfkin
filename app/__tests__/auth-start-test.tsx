import { act, waitFor } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import * as SplashScreen from 'expo-splash-screen';

import { setupApiServer } from '../test-utils/api';
import { mockFirebase, signedInApiHandlers, signInTestUser } from '../test-utils/auth';

jest.setTimeout(60000);

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

jest.mock('expo-splash-screen', () => ({
  preventAutoHideAsync: jest.fn(() => Promise.resolve()),
  hide: jest.fn(),
  hideAsync: jest.fn(() => Promise.resolve()),
}));

setupApiServer(...signedInApiHandlers());

// Only the first test in this file sees Firebase still restoring the session:
// the auth state module starts listening once per test file.
test('while the session is restored a loading screen is shown, and a signed-in user never sees /sign-in', async () => {
  signInTestUser();
  mockFirebase.holdRestore();

  await renderRouter('./src/app', { initialUrl: '/settings' });

  expect(screen.getByTestId('auth-loading')).toBeTruthy();
  expect(screen.queryByRole('heading', { name: 'Sign in' })).toBeNull();
  expect(SplashScreen.hide).not.toHaveBeenCalled();

  await act(async () => {
    mockFirebase.finishRestore();
  });

  await waitFor(() => expect(screen.getByRole('heading', { name: 'Settings' })).toBeTruthy());
  expect(screen).toHavePathname('/settings');
  expect(SplashScreen.hide).toHaveBeenCalled();
});

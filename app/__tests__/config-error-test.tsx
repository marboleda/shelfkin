import { renderRouter, screen } from 'expo-router/testing-library';

jest.setTimeout(60000);

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

// A production build with no EXPO_PUBLIC_API_URL.
jest.mock('@/api/config', () => {
  const actual = jest.requireActual('@/api/config');
  return { ...actual, apiConfig: actual.resolveApiConfig(undefined, false) };
});

const fetchSpy = jest.fn();

beforeEach(() => {
  fetchSpy.mockReset();
  globalThis.fetch = fetchSpy;
});

test('a production build without an API URL shows a configuration error instead of the app', async () => {
  await renderRouter('./src/app', { initialUrl: '/settings' });

  expect(screen.getByRole('heading', { name: 'App not configured' })).toBeTruthy();
  expect(screen.getByTestId('config-error')).toHaveTextContent(/EXPO_PUBLIC_API_URL/);
  expect(screen.queryByTestId('server-status')).toBeNull();
  expect(fetchSpy).not.toHaveBeenCalled();
});

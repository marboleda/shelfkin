import { act, fireEvent } from '@testing-library/react-native';
import { renderRouter, screen, testRouter } from 'expo-router/testing-library';
import { Dimensions } from 'react-native';
import { setupApiServer } from '../test-utils/api';
import { householdsHandler, mockFirebase, signedInApiHandlers, signInTestUser } from '../test-utils/auth';

jest.setTimeout(60000);

// These tests render the signed-in app: sign the test user in and answer its API calls.
const server = setupApiServer(...signedInApiHandlers());
beforeEach(() => {
  signInTestUser();
});

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

// Render the real routes in src/app, including the root and (app) layouts.
const APP_DIR = './src/app';

async function setWindowWidth(width: number) {
  const metrics = { width, height: 900, scale: 1, fontScale: 1 };
  await act(async () => {
    Dimensions.set({ window: metrics, screen: metrics });
  });
}

beforeEach(async () => {
  await setWindowWidth(390);
});

describe('routes', () => {
  test('the root layout renders the Pantry route at /', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(screen).toHavePathname('/');
    expect(screen.getByLabelText('Switch household, current: Home')).toBeTruthy();
  });

  test.each([
    ['/add', 'Add item'],
    ['/settings', 'Settings'],
    ['/entries/abc', 'Entry detail'],
  ])('%s opens the %s placeholder directly', async (url, heading) => {
    await renderRouter(APP_DIR, { initialUrl: url });

    expect(screen).toHavePathname(url);
    expect(screen.getByRole('heading', { name: heading })).toBeTruthy();
  });

  test('/onboarding opens directly for a user with no household', async () => {
    server.use(householdsHandler([]));
    await renderRouter(APP_DIR, { initialUrl: '/onboarding' });

    expect(screen).toHavePathname('/onboarding');
    expect(await screen.findByRole('heading', { name: 'Set up your pantry' })).toBeTruthy();
  });

  test('entry detail shows the id from the URL', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/entries/abc' });

    expect(screen.getByTestId('entry-id')).toHaveTextContent('abc');
  });

  test.each(['/sign-in', '/sign-up', '/onboarding'])('%s has no tab bar or side nav', async (url) => {
    if (url === '/onboarding') server.use(householdsHandler([]));
    else mockFirebase.signOutElsewhere();
    await renderRouter(APP_DIR, { initialUrl: url });
    expect(screen).toHavePathname(url);

    expect(screen.queryByTestId('bottom-tab-bar')).toBeNull();
    expect(screen.queryByTestId('side-nav')).toBeNull();
  });

  test('an unknown URL shows Page not found with a link back to Pantry', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/nope' });

    expect(screen.getByRole('heading', { name: 'Page not found' })).toBeTruthy();

    await act(async () => {
      fireEvent.press(screen.getByText('Go to Pantry'));
    });
    expect(screen).toHavePathname('/');
    expect(screen.getByLabelText('Switch household, current: Home')).toBeTruthy();
  });
});

describe('Pantry navigation', () => {
  test('the Add item button opens Add item, and its back control returns to Pantry', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/' });

    await act(async () => {
      fireEvent.press(screen.getByText('Add item'));
    });
    expect(screen).toHavePathname('/add');
    expect(screen.getByRole('heading', { name: 'Add item' })).toBeTruthy();

    await act(async () => {
      fireEvent.press(screen.getByLabelText('Back to Pantry'));
    });
    expect(screen).toHavePathname('/');
  });

  test('the sample row opens /entries/sample, and its back control returns to Pantry', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/' });

    await act(async () => {
      fireEvent.press(screen.getByText('Sample entry'));
    });
    expect(screen).toHavePathname('/entries/sample');
    expect(screen.getByTestId('entry-id')).toHaveTextContent('sample');

    await act(async () => {
      fireEvent.press(screen.getByLabelText('Back to Pantry'));
    });
    expect(screen).toHavePathname('/');
  });

  test('a directly opened Add item screen still has a back control to Pantry', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/add' });

    await act(async () => {
      fireEvent.press(screen.getByLabelText('Back to Pantry'));
    });
    expect(screen).toHavePathname('/');
  });

  // The Android hardware back button dispatches a goBack on the focused navigator.
  test.each(['/add', '/entries/sample'])('going back from %s returns to Pantry', async (url) => {
    await renderRouter(APP_DIR, { initialUrl: '/' });
    await testRouter.push(url);
    expect(screen).toHavePathname(url);

    await testRouter.back();
    expect(screen).toHavePathname('/');
  });

  test('going back from a directly opened entry returns to Pantry', async () => {
    await renderRouter(APP_DIR, { initialUrl: '/entries/abc' });
    expect(testRouter.canGoBack()).toBe(true);

    await testRouter.back();
    expect(screen).toHavePathname('/');
  });
});

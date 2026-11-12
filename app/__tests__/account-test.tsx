import { act, fireEvent, waitFor } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { http, HttpResponse } from 'msw';

import { apiUrl, setupApiServer } from '../test-utils/api';
import { signInTestUser, TEST_EMAIL, testMe } from '../test-utils/auth';

jest.setTimeout(60000);

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

const mockTimeZone = jest.fn(() => 'America/Toronto');
jest.mock('@/auth/timezone', () => ({ deviceTimeZone: () => mockTimeZone() }));

const APP_DIR = './src/app';
const server = setupApiServer(http.get(apiUrl('/health'), () => HttpResponse.json({ status: 'ok' })));

type Me = ReturnType<typeof testMe>;

/** A fake API server profile: GET /me returns it and PATCH /me changes it, recording each request. */
function fakeProfile(initial: Me = testMe()) {
  const api = { me: initial, gets: 0, patches: [] as Partial<Me>[] };
  server.use(
    http.get(apiUrl('/me'), () => {
      api.gets += 1;
      return HttpResponse.json(api.me);
    }),
    http.patch(apiUrl('/me'), async ({ request }) => {
      const body = (await request.json()) as Partial<Me>;
      api.patches.push(body);
      api.me = { ...api.me, ...body };
      return HttpResponse.json(api.me);
    }),
  );
  return api;
}

beforeEach(() => {
  mockTimeZone.mockReturnValue('America/Toronto');
  signInTestUser();
});

describe('profile sync', () => {
  test('on start while signed in, the app calls GET /me and sends nothing when the time zone matches', async () => {
    const api = fakeProfile(testMe({ timezone: 'America/Toronto' }));

    await renderRouter(APP_DIR, { initialUrl: '/' });

    await waitFor(() => expect(api.gets).toBeGreaterThan(0));
    expect(api.patches).toEqual([]);
  });

  test('when the device time zone differs from the stored one, it sends PATCH /me with the new one', async () => {
    mockTimeZone.mockReturnValue('Asia/Tokyo');
    const api = fakeProfile(testMe({ timezone: 'America/Toronto' }));

    await renderRouter(APP_DIR, { initialUrl: '/' });

    await waitFor(() => expect(api.patches).toEqual([{ timezone: 'Asia/Tokyo' }]));
  });
});

describe('Account section', () => {
  async function openSettings() {
    await renderRouter(APP_DIR, { initialUrl: '/settings' });
    await waitFor(() => expect(screen.getByTestId('account-name-value')).toHaveTextContent('Sam'));
  }

  test('shows the name and email', async () => {
    fakeProfile();
    await openSettings();

    expect(screen.getByRole('heading', { name: 'Account' })).toBeTruthy();
    expect(screen.getByTestId('account-email')).toHaveTextContent(TEST_EMAIL);
  });

  test('an invalid name shows a message and sends nothing', async () => {
    const api = fakeProfile();
    await openSettings();

    await fireEvent.press(screen.getByTestId('account-name'));
    await fireEvent.changeText(screen.getByTestId('account-name-input'), '   ');
    await act(async () => {
      await fireEvent.press(screen.getByRole('button', { name: 'Save' }));
    });

    expect(screen.getByText('Enter your name')).toBeTruthy();
    expect(api.patches).toEqual([]);
  });

  test('saving sends PATCH /me, and the new name is still shown after a reload', async () => {
    const api = fakeProfile();
    await openSettings();

    await fireEvent.press(screen.getByTestId('account-name'));
    expect(screen.getByTestId('account-name-input')).toHaveProp('value', 'Sam');
    await fireEvent.changeText(screen.getByTestId('account-name-input'), '  Sammy  ');
    await act(async () => {
      await fireEvent.press(screen.getByRole('button', { name: 'Save' }));
    });

    await waitFor(() => expect(screen.getByTestId('account-name-value')).toHaveTextContent('Sammy'));
    expect(api.patches).toEqual([{ displayName: 'Sammy' }]);

    // Reload: a new app run reads the name from the API again.
    await renderRouter(APP_DIR, { initialUrl: '/settings' });
    await waitFor(() => expect(screen.getByTestId('account-name-value')).toHaveTextContent('Sammy'));
  });

  test('a failed save shows a readable message and keeps the form open', async () => {
    fakeProfile();
    server.use(
      http.patch(apiUrl('/me'), () =>
        HttpResponse.json({ error: { code: 'internal', message: 'Something went wrong.' } }, { status: 500 }),
      ),
    );
    await openSettings();

    await fireEvent.press(screen.getByTestId('account-name'));
    await fireEvent.changeText(screen.getByTestId('account-name-input'), 'Sammy');
    await act(async () => {
      await fireEvent.press(screen.getByRole('button', { name: 'Save' }));
    });

    await waitFor(() => expect(screen.getByText(/The server ran into a problem/)).toBeTruthy());
    expect(screen.getByTestId('account-name-input')).toHaveProp('value', 'Sammy');
  });
});

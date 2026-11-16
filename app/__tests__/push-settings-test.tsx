import { act, fireEvent, waitFor } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import * as Notifications from 'expo-notifications';
import { http, HttpResponse } from 'msw';
import { Linking, Platform } from 'react-native';

import { BLOCKED_MESSAGE } from '@/push/use-push-settings';
import { loadDevicePush, saveDevicePush } from '@/push/storage';
import { apiUrl, setupApiServer } from '../test-utils/api';
import { householdsHandler, mockFirebase, signInTestUser, TEST_EMAIL, TEST_PASSWORD, testHousehold, testMe } from '../test-utils/auth';

import { mockNotifications } from '../jest/expo-notifications-mock';

jest.setTimeout(60000);

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

jest.mock('@/auth/timezone', () => ({ deviceTimeZone: () => 'America/Toronto' }));

const APP_DIR = './src/app';
const TOKEN = 'ExponentPushToken[device-token-1]';
const ENCODED_TOKEN = 'ExponentPushToken%5Bdevice-token-1%5D';

const HOME = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000a', name: 'Home', role: 'owner' });
const CABIN = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000b', name: 'Cabin', role: 'member' });

type Me = ReturnType<typeof testMe>;

const server = setupApiServer(
  http.get(apiUrl('/health'), () => HttpResponse.json({ status: 'ok' })),
  householdsHandler([CABIN, HOME]),
);

/** A fake API: `GET` / `PATCH /me` and the push token endpoints, recording every change in order. */
function fakeApi(initial: Me = testMe()) {
  const api = {
    me: initial,
    log: [] as string[],
    /** Whether the user was still signed in to Firebase when each DELETE arrived. */
    deletesWhileSignedIn: [] as boolean[],
  };
  server.use(
    http.get(apiUrl('/me'), () => HttpResponse.json(api.me)),
    http.patch(apiUrl('/me'), async ({ request }) => {
      const body = (await request.json()) as Partial<Me>;
      api.log.push(`PATCH /me ${JSON.stringify(body)}`);
      api.me = { ...api.me, ...body };
      return HttpResponse.json(api.me);
    }),
    http.post(apiUrl('/me/push-tokens'), async ({ request }) => {
      const body = (await request.json()) as { token: string; platform: 'ios' | 'android' };
      api.log.push(`POST ${body.token} ${body.platform}`);
      return HttpResponse.json({ ...body, createdAt: '2026-11-14T09:00:00Z', lastSeenAt: '2026-11-14T09:00:00Z' });
    }),
    http.delete(apiUrl('/me/push-tokens/:token'), ({ request }) => {
      api.deletesWhileSignedIn.push(mockFirebase.currentUser !== null);
      api.log.push(`DELETE ${new URL(request.url).pathname}`);
      return new HttpResponse(null, { status: 204 });
    }),
  );
  return api;
}

let uid: string;
beforeEach(() => {
  uid = signInTestUser().uid;
});

afterEach(() => {
  jest.restoreAllMocks();
});

async function openSettings() {
  await renderRouter(APP_DIR, { initialUrl: '/settings' });
  await waitFor(() => expect(screen.getByTestId('push-account-switch')).toBeEnabled());
}

async function toggle(testID: string, value: boolean) {
  await act(async () => {
    await fireEvent(screen.getByTestId(testID), 'valueChange', value);
  });
}

function accountSwitch() {
  return screen.getByTestId('push-account-switch');
}

function deviceSwitch() {
  return screen.queryByTestId('push-device-switch');
}

describe('Notifications section', () => {
  test('shows the account switch with its note, reading pushEnabled from GET /me', async () => {
    fakeApi(testMe({ pushEnabled: false }));
    await openSettings();

    expect(screen.getByRole('heading', { name: 'Notifications' })).toBeTruthy();
    expect(screen.getByText('Expiry reminders')).toBeTruthy();
    expect(screen.getByText('A daily summary at about 9am when items are about to expire')).toBeTruthy();
    expect(accountSwitch()).toHaveProp('value', false);
    // The device switch only shows while the account switch is on.
    expect(deviceSwitch()).toBeNull();
    expect(screen.queryByTestId('push-web-note')).toBeNull();
    // activeThumbColor is a react-native-web prop: phones get thumbColor alone.
    expect(accountSwitch().props.activeThumbColor).toBeUndefined();
  });

  test('turning it on asks for permission, registers the token, sets pushEnabled, and both switches show on', async () => {
    const api = fakeApi();
    await openSettings();

    await toggle('push-account-switch', true);

    await waitFor(() => expect(api.log).toEqual([`POST ${TOKEN} ios`, 'PATCH /me {"pushEnabled":true}']));
    expect(Notifications.requestPermissionsAsync).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(deviceSwitch()).toHaveProp('value', true));
    expect(accountSwitch()).toHaveProp('value', true);
  });

  test('when permission is denied, nothing is sent, the switch goes back off and the blocked message offers Settings', async () => {
    mockNotifications.answerPrompt('denied');
    const openSettingsSpy = jest.spyOn(Linking, 'openSettings').mockResolvedValue(undefined);
    const api = fakeApi();
    await openSettings();

    await toggle('push-account-switch', true);

    await waitFor(() => expect(screen.getByText(BLOCKED_MESSAGE)).toBeTruthy());
    expect(BLOCKED_MESSAGE).toBe('Notifications are blocked for Shelfkin. Turn them on in Settings.');
    expect(api.log).toEqual([]);
    expect(accountSwitch()).toHaveProp('value', false);
    expect(deviceSwitch()).toBeNull();

    await act(async () => {
      await fireEvent.press(screen.getByRole('button', { name: 'Open Settings' }));
    });
    expect(openSettingsSpy).toHaveBeenCalledTimes(1);
  });

  test('when permission was denied earlier, it shows the same message without asking', async () => {
    mockNotifications.setPermission('blocked');
    const api = fakeApi();
    await openSettings();

    await toggle('push-account-switch', true);

    await waitFor(() => expect(screen.getByText(BLOCKED_MESSAGE)).toBeTruthy());
    expect(Notifications.requestPermissionsAsync).not.toHaveBeenCalled();
    expect(api.log).toEqual([]);
    expect(accountSwitch()).toHaveProp('value', false);
  });

  test('without an EAS project ID, it says so, sends nothing and the switch goes back off', async () => {
    const Constants = jest.requireActual('expo-constants').default;
    Constants.expoConfig.extra = {};
    const api = fakeApi();
    await openSettings();

    await toggle('push-account-switch', true);

    await waitFor(() => expect(screen.getByTestId('push-error')).toBeTruthy());
    expect(api.log).toEqual([]);
    expect(accountSwitch()).toHaveProp('value', false);
  });

  test('"On this device" off deletes the URL-encoded token and keeps the account on; on registers it again', async () => {
    mockNotifications.setPermission('granted');
    await saveDevicePush(uid, { enabled: true, token: TOKEN });
    const api = fakeApi(testMe({ pushEnabled: true }));
    await openSettings();
    await waitFor(() => expect(deviceSwitch()).toHaveProp('value', true));
    // Start-up registration.
    await waitFor(() => expect(api.log).toEqual([`POST ${TOKEN} ios`]));
    api.log.length = 0;

    await toggle('push-device-switch', false);

    await waitFor(() => expect(deviceSwitch()).toBeEnabled());
    expect(deviceSwitch()).toHaveProp('value', false);
    expect(api.log).toEqual([`DELETE /me/push-tokens/${ENCODED_TOKEN}`]);
    expect(accountSwitch()).toHaveProp('value', true);

    await toggle('push-device-switch', true);

    await waitFor(() => expect(deviceSwitch()).toBeEnabled());
    expect(deviceSwitch()).toHaveProp('value', true);
    expect(api.log).toEqual([`DELETE /me/push-tokens/${ENCODED_TOKEN}`, `POST ${TOKEN} ios`]);
  });

  test('turning the account off sets pushEnabled false and keeps the token', async () => {
    mockNotifications.setPermission('granted');
    await saveDevicePush(uid, { enabled: true, token: TOKEN });
    const api = fakeApi(testMe({ pushEnabled: true }));
    await openSettings();
    await waitFor(() => expect(api.log).toEqual([`POST ${TOKEN} ios`]));
    api.log.length = 0;

    await toggle('push-account-switch', false);

    await waitFor(() => expect(api.log).toEqual(['PATCH /me {"pushEnabled":false}']));
    await waitFor(() => expect(deviceSwitch()).toBeNull());
    expect(await loadDevicePush(uid)).toEqual({ enabled: true, token: TOKEN });
  });

  test('after a restart with notifications blocked in the OS, the device switch shows off with a note', async () => {
    mockNotifications.setPermission('blocked');
    await saveDevicePush(uid, { enabled: true, token: TOKEN });
    const api = fakeApi(testMe({ pushEnabled: true }));
    await openSettings();

    await waitFor(() => expect(screen.getByTestId('push-device-note')).toHaveTextContent('Blocked in system settings'));
    expect(deviceSwitch()).toHaveProp('value', false);
    expect(accountSwitch()).toHaveProp('value', true);
    // No start-up registration without permission.
    expect(api.log).toEqual([]);
  });

  test('on a simulator, the device switch is disabled and says it needs a physical phone', async () => {
    const { mockDevice } = jest.requireActual('../jest/expo-device-mock');
    mockDevice.setIsDevice(false);
    fakeApi(testMe({ pushEnabled: true }));
    await openSettings();

    await waitFor(() => expect(screen.getByTestId('push-device-note')).toHaveTextContent('Needs a physical phone'));
    expect(deviceSwitch()).toBeDisabled();
  });

  test('on web, only the account switch shows, with its note, and turning it on asks for nothing', async () => {
    jest.replaceProperty(Platform, 'OS', 'web');
    const api = fakeApi();
    await openSettings();

    expect(screen.getByTestId('push-web-note')).toHaveTextContent('Reminders are sent to the Shelfkin app on your phone');
    // react-native-web would otherwise paint the thumb of a switch that is on in its own teal.
    expect(accountSwitch().props.activeThumbColor).toBeTruthy();
    await toggle('push-account-switch', true);

    await waitFor(() => expect(api.log).toEqual(['PATCH /me {"pushEnabled":true}']));
    await waitFor(() => expect(accountSwitch()).toHaveProp('value', true));
    expect(deviceSwitch()).toBeNull();
    expect(Notifications.getPermissionsAsync).not.toHaveBeenCalled();
    expect(Notifications.requestPermissionsAsync).not.toHaveBeenCalled();
  });
});

describe('app start', () => {
  test('re-registers the token when the account, the device and the permission are all on', async () => {
    mockNotifications.setPermission('granted');
    await saveDevicePush(uid, { enabled: true, token: TOKEN });
    const api = fakeApi(testMe({ pushEnabled: true }));

    await renderRouter(APP_DIR, { initialUrl: '/' });

    await waitFor(() => expect(api.log).toEqual([`POST ${TOKEN} ios`]));
  });

  test('registers nothing when the account switch is off', async () => {
    mockNotifications.setPermission('granted');
    await saveDevicePush(uid, { enabled: true, token: TOKEN });
    const api = fakeApi(testMe({ pushEnabled: false }));

    await renderRouter(APP_DIR, { initialUrl: '/' });
    await screen.findByLabelText('Switch household, current: Cabin');

    expect(api.log).toEqual([]);
  });

  test('a new token from the OS replaces the old one', async () => {
    mockNotifications.setPermission('granted');
    await saveDevicePush(uid, { enabled: true, token: TOKEN });
    const api = fakeApi(testMe({ pushEnabled: true }));
    await renderRouter(APP_DIR, { initialUrl: '/' });
    await waitFor(() => expect(api.log).toEqual([`POST ${TOKEN} ios`]));

    await act(async () => {
      mockNotifications.changeToken('ExponentPushToken[device-token-2]');
    });

    await waitFor(() =>
      expect(api.log).toEqual([
        `POST ${TOKEN} ios`,
        'POST ExponentPushToken[device-token-2] ios',
        `DELETE /me/push-tokens/${ENCODED_TOKEN}`,
      ]),
    );
  });

  test('a reminder that arrives while the app is open is shown as a banner', () => {
    // Set when the push module loads (the root layout imports it).
    let setHandler: jest.Mock | undefined;
    jest.isolateModules(() => {
      setHandler = require('expo-notifications').setNotificationHandler;
      require('@/push/push-lifecycle');
    });
    const handler = setHandler?.mock.calls.at(-1)?.[0];
    expect(handler).toBeTruthy();
    return expect(handler.handleNotification()).resolves.toMatchObject({ shouldShowBanner: true, shouldShowList: true });
  });
});

describe('sign-out', () => {
  async function signOutFromSettings() {
    await act(async () => {
      await fireEvent.press(screen.getByRole('button', { name: 'Sign out' }));
    });
    await waitFor(() => expect(screen).toHavePathname('/sign-in'));
  }

  test('deletes this device token before signing out of Firebase', async () => {
    mockNotifications.setPermission('granted');
    await saveDevicePush(uid, { enabled: true, token: TOKEN });
    const api = fakeApi(testMe({ pushEnabled: true }));
    await openSettings();
    await waitFor(() => expect(api.log).toEqual([`POST ${TOKEN} ios`]));

    await signOutFromSettings();

    expect(api.log).toEqual([`POST ${TOKEN} ios`, `DELETE /me/push-tokens/${ENCODED_TOKEN}`]);
    expect(api.deletesWhileSignedIn).toEqual([true]);
    expect(mockFirebase.currentUser).toBeNull();
  });

  test('still signs out when the delete fails', async () => {
    const warn = jest.spyOn(console, 'warn').mockImplementation(() => {});
    await saveDevicePush(uid, { enabled: true, token: TOKEN });
    fakeApi(testMe({ pushEnabled: false }));
    server.use(http.delete(apiUrl('/me/push-tokens/:token'), () => HttpResponse.error()));
    await openSettings();

    await signOutFromSettings();

    expect(mockFirebase.currentUser).toBeNull();
    warn.mockRestore();
  });
});

describe('deleting the account', () => {
  test('sends no push token DELETE (the API already deleted the tokens) and forgets the device token', async () => {
    mockNotifications.setPermission('granted');
    await saveDevicePush(uid, { enabled: true, token: TOKEN });
    const api = fakeApi(testMe({ pushEnabled: true }));
    server.use(
      http.delete(apiUrl('/me'), () => {
        api.log.push('DELETE /me');
        return new HttpResponse(null, { status: 204 });
      }),
    );
    await openSettings();
    await waitFor(() => expect(api.log).toEqual([`POST ${TOKEN} ios`]));

    await act(async () => {
      await fireEvent.press(screen.getByRole('button', { name: 'Delete account' }));
    });
    await fireEvent.changeText(screen.getByTestId('delete-account-dialog-input'), 'DELETE');
    await act(async () => {
      await fireEvent.press(screen.getByTestId('delete-account-dialog-confirm'));
    });

    await waitFor(() => expect(screen).toHavePathname('/sign-in'));
    expect(api.log).toEqual([`POST ${TOKEN} ios`, 'DELETE /me']);
    expect(await loadDevicePush(uid)).toEqual({ enabled: false, token: null });
    expect(mockFirebase.currentUser).toBeNull();
  });
});

describe('tapping a reminder', () => {
  async function pantryFor(name: string) {
    await waitFor(() => expect(screen).toHavePathname('/'));
    expect(await screen.findByLabelText(`Switch household, current: ${name}`)).toBeTruthy();
  }

  test('that opened the app from closed opens Pantry for its household and makes it current', async () => {
    fakeApi();
    mockNotifications.tap(HOME.id, { title: 'Expiring soon in Home' });

    await renderRouter(APP_DIR, { initialUrl: '/settings' });

    await pantryFor('Home');
    expect(Notifications.clearLastNotificationResponseAsync).toHaveBeenCalled();
  });

  test('while the app is running in the background, switches household and opens Pantry', async () => {
    fakeApi();
    await openSettings();

    await act(async () => {
      mockNotifications.tap(HOME.id, { whileRunning: true });
    });

    await pantryFor('Home');
  });

  test('for a household the user is no longer in, opens the current Pantry with the no-access message', async () => {
    fakeApi();
    mockNotifications.tap('0190f3a1-0000-7000-8000-0000000000ff', { title: 'Expiring soon in Old Flat' });

    await renderRouter(APP_DIR, { initialUrl: '/settings' });

    await pantryFor('Cabin');
    expect(await screen.findByText('You no longer have access to Old Flat')).toBeTruthy();
  });

  test('while signed out, opens after sign-in', async () => {
    fakeApi();
    mockFirebase.reset();
    mockFirebase.addAccount(TEST_EMAIL, TEST_PASSWORD);
    mockNotifications.tap(HOME.id);
    await renderRouter(APP_DIR, { initialUrl: '/' });
    expect(screen).toHavePathname('/sign-in');

    await fireEvent.changeText(screen.getByTestId('email-input'), TEST_EMAIL);
    await fireEvent.changeText(screen.getByTestId('password-input'), TEST_PASSWORD);
    await act(async () => {
      fireEvent.press(screen.getByRole('button', { name: 'Sign in' }));
    });

    await pantryFor('Home');
  });

  test('is handled once', async () => {
    fakeApi();
    mockNotifications.tap(HOME.id);
    await renderRouter(APP_DIR, { initialUrl: '/' });
    await pantryFor('Home');

    // A later app render does not switch back to the tapped household.
    await act(async () => {
      fireEvent.press(screen.getByLabelText('Switch household, current: Home'));
    });
    await act(async () => {
      fireEvent.press(await screen.findByRole('button', { name: /Cabin/ }));
    });
    await pantryFor('Cabin');
  });
});

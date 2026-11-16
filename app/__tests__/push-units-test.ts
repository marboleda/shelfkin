import Constants from 'expo-constants';
import { http, HttpResponse } from 'msw';
import { Platform } from 'react-native';

import { queryKeys } from '@/api/query-keys';
import { ANDROID_CHANNEL_ID, ANDROID_CHANNEL_NAME, getPushPermission, requestPushPermission } from '@/push/device';
import {
  registerOnStart,
  turnOffAccount,
  turnOffDevice,
  turnOnAccount,
  turnOnDevice,
  unregisterBeforeSignOut,
} from '@/push/registration';
import { loadDevicePush, saveDevicePush } from '@/push/storage';
import { reminderTarget } from '@/push/taps';
import * as Notifications from 'expo-notifications';
import { apiUrl, createTestQueryClient, setupApiServer } from '../test-utils/api';
import { signInTestUser, testMe } from '../test-utils/auth';

import { mockDevice } from '../jest/expo-device-mock';
import { mockNotifications } from '../jest/expo-notifications-mock';

const TOKEN = 'ExponentPushToken[device-token-1]';
const ENCODED_TOKEN = 'ExponentPushToken%5Bdevice-token-1%5D';

type Me = ReturnType<typeof testMe>;

/** A fake API: records push token requests and `PATCH /me` bodies. */
function fakeApi(initial: Me = testMe()) {
  const api = {
    me: initial,
    patches: [] as Partial<Me>[],
    registered: [] as { token: string; platform: string }[],
    /** Raw request paths of each DELETE, to check the encoding. */
    deletedPaths: [] as string[],
    log: [] as string[],
  };
  server.use(
    http.patch(apiUrl('/me'), async ({ request }) => {
      const body = (await request.json()) as Partial<Me>;
      api.patches.push(body);
      api.log.push(`PATCH /me ${JSON.stringify(body)}`);
      api.me = { ...api.me, ...body };
      return HttpResponse.json(api.me);
    }),
    http.post(apiUrl('/me/push-tokens'), async ({ request }) => {
      const body = (await request.json()) as { token: string; platform: 'ios' | 'android' };
      api.registered.push(body);
      api.log.push(`POST ${body.token}`);
      return HttpResponse.json({ ...body, createdAt: '2026-11-14T09:00:00Z', lastSeenAt: '2026-11-14T09:00:00Z' });
    }),
    http.delete(apiUrl('/me/push-tokens/:token'), ({ request }) => {
      const path = new URL(request.url).pathname;
      api.deletedPaths.push(path);
      api.log.push(`DELETE ${path}`);
      return new HttpResponse(null, { status: 204 });
    }),
  );
  return api;
}

const server = setupApiServer();
let uid: string;

beforeEach(() => {
  uid = signInTestUser().uid;
});

afterEach(() => {
  jest.restoreAllMocks();
});

describe('turning the account switch on', () => {
  test('on a phone, with permission granted, registers the token and then sets pushEnabled', async () => {
    const api = fakeApi();
    const queryClient = createTestQueryClient();

    const result = await turnOnAccount(queryClient, uid);

    expect(result).toEqual({ ok: true });
    expect(Notifications.requestPermissionsAsync).toHaveBeenCalledTimes(1);
    expect(api.log).toEqual([`POST ${TOKEN}`, 'PATCH /me {"pushEnabled":true}']);
    expect(api.registered).toEqual([{ token: TOKEN, platform: 'ios' }]);
    expect(queryClient.getQueryData<Me>(queryKeys.me())?.pushEnabled).toBe(true);
    expect(await loadDevicePush(uid)).toEqual({ enabled: true, token: TOKEN });
    // The token is asked for with the build's project ID.
    expect(Notifications.getExpoPushTokenAsync).toHaveBeenCalledWith({
      projectId: '00000000-0000-4000-8000-000000000034',
    });
  });

  test('on Android, registers with platform android and creates the channel before asking', async () => {
    jest.replaceProperty(Platform, 'OS', 'android');
    const api = fakeApi();

    await turnOnAccount(createTestQueryClient(), uid);

    expect(Notifications.setNotificationChannelAsync).toHaveBeenCalledWith(ANDROID_CHANNEL_ID, {
      name: ANDROID_CHANNEL_NAME,
      importance: Notifications.AndroidImportance.HIGH,
    });
    const channelOrder = (Notifications.setNotificationChannelAsync as jest.Mock).mock.invocationCallOrder[0];
    const requestOrder = (Notifications.requestPermissionsAsync as jest.Mock).mock.invocationCallOrder[0];
    expect(channelOrder).toBeLessThan(requestOrder);
    expect(api.registered).toEqual([{ token: TOKEN, platform: 'android' }]);
  });

  test('when the user denies the permission, nothing is sent to the API', async () => {
    mockNotifications.answerPrompt('denied');
    const api = fakeApi();

    const result = await turnOnAccount(createTestQueryClient(), uid);

    expect(result).toEqual({ ok: false, reason: 'blocked' });
    expect(api.log).toEqual([]);
    expect(await loadDevicePush(uid)).toEqual({ enabled: false, token: null });
  });

  test('when permission was denied earlier, it does not ask again and sends nothing', async () => {
    mockNotifications.setPermission('blocked');
    const api = fakeApi();

    const result = await turnOnAccount(createTestQueryClient(), uid);

    expect(result).toEqual({ ok: false, reason: 'blocked' });
    expect(Notifications.requestPermissionsAsync).not.toHaveBeenCalled();
    expect(api.log).toEqual([]);
  });

  test('Android 13 may ask again after one denial', async () => {
    mockNotifications.setPermission('deniedCanAsk');
    expect(await getPushPermission()).toBe('undetermined');
    expect(await requestPushPermission()).toBe('granted');
  });

  test('provisional authorization on iOS counts as granted', async () => {
    (Notifications.getPermissionsAsync as jest.Mock).mockResolvedValueOnce({
      status: 'granted',
      granted: false,
      canAskAgain: true,
      expires: 'never',
      ios: { status: Notifications.IosAuthorizationStatus.PROVISIONAL },
    });
    expect(await getPushPermission()).toBe('granted');
  });

  test('without an EAS project ID in the build, it fails gracefully and sends nothing', async () => {
    Constants.expoConfig!.extra = {};
    const api = fakeApi();

    const result = await turnOnAccount(createTestQueryClient(), uid);

    expect(result).toEqual({ ok: false, reason: 'not_configured' });
    expect(Notifications.getExpoPushTokenAsync).not.toHaveBeenCalled();
    expect(api.log).toEqual([]);
  });

  test('when Expo cannot return a token, it fails gracefully and sends nothing', async () => {
    mockNotifications.failToken(new Error('offline'));
    const api = fakeApi();

    expect(await turnOnAccount(createTestQueryClient(), uid)).toEqual({ ok: false, reason: 'token_failed' });
    expect(api.log).toEqual([]);
  });

  test('on a simulator, only pushEnabled is set, with no prompt and no token', async () => {
    mockDevice.setIsDevice(false);
    const api = fakeApi();

    expect(await turnOnAccount(createTestQueryClient(), uid)).toEqual({ ok: true });
    expect(Notifications.requestPermissionsAsync).not.toHaveBeenCalled();
    expect(api.log).toEqual(['PATCH /me {"pushEnabled":true}']);
  });

  test('on web, only pushEnabled is set, with no prompt and no token', async () => {
    jest.replaceProperty(Platform, 'OS', 'web');
    const api = fakeApi();

    expect(await turnOnAccount(createTestQueryClient(), uid)).toEqual({ ok: true });
    expect(Notifications.getPermissionsAsync).not.toHaveBeenCalled();
    expect(Notifications.requestPermissionsAsync).not.toHaveBeenCalled();
    expect(api.log).toEqual(['PATCH /me {"pushEnabled":true}']);
  });
});

describe('turning the account switch off', () => {
  test('sets pushEnabled false and keeps the tokens', async () => {
    const api = fakeApi(testMe({ pushEnabled: true }));
    await saveDevicePush(uid, { enabled: true, token: TOKEN });

    await turnOffAccount(createTestQueryClient());

    expect(api.log).toEqual(['PATCH /me {"pushEnabled":false}']);
    expect(await loadDevicePush(uid)).toEqual({ enabled: true, token: TOKEN });
  });
});

describe('the device switch', () => {
  test('off deletes the URL-encoded token and leaves the account switch alone', async () => {
    const api = fakeApi(testMe({ pushEnabled: true }));
    await saveDevicePush(uid, { enabled: true, token: TOKEN });

    await turnOffDevice(uid);

    expect(api.deletedPaths).toEqual([`/me/push-tokens/${ENCODED_TOKEN}`]);
    expect(api.patches).toEqual([]);
    expect(await loadDevicePush(uid)).toEqual({ enabled: false, token: null });
  });

  test('on registers the token again', async () => {
    mockNotifications.setPermission('granted');
    const api = fakeApi(testMe({ pushEnabled: true }));
    await saveDevicePush(uid, { enabled: false, token: null });

    expect(await turnOnDevice(uid)).toEqual({ ok: true });

    expect(api.log).toEqual([`POST ${TOKEN}`]);
    expect(await loadDevicePush(uid)).toEqual({ enabled: true, token: TOKEN });
  });

  test('a failed delete keeps the device on', async () => {
    fakeApi(testMe({ pushEnabled: true }));
    server.use(http.delete(apiUrl('/me/push-tokens/:token'), () => HttpResponse.error()));
    await saveDevicePush(uid, { enabled: true, token: TOKEN });

    await expect(turnOffDevice(uid)).rejects.toMatchObject({ kind: 'network' });
    expect(await loadDevicePush(uid)).toEqual({ enabled: true, token: TOKEN });
  });

  test('the preference is kept per user', async () => {
    await saveDevicePush(uid, { enabled: true, token: TOKEN });
    expect(await loadDevicePush('someone-else')).toEqual({ enabled: false, token: null });
  });
});

describe('registering on app start', () => {
  test('registers again when the account switch, the device switch and the permission are all on', async () => {
    mockNotifications.setPermission('granted');
    const api = fakeApi();
    await saveDevicePush(uid, { enabled: true, token: TOKEN });

    expect(await registerOnStart(uid, { pushEnabled: true })).toBe(true);

    expect(api.log).toEqual([`POST ${TOKEN}`]);
  });

  test.each([
    ['the account switch is off', { pushEnabled: false }, { enabled: true, token: TOKEN }, 'granted'],
    ['the device switch is off', { pushEnabled: true }, { enabled: false, token: null }, 'granted'],
    ['the permission is blocked', { pushEnabled: true }, { enabled: true, token: TOKEN }, 'blocked'],
    ['the permission is not decided', { pushEnabled: true }, { enabled: true, token: TOKEN }, 'undetermined'],
  ] as const)('does nothing when %s', async (_name, me, stored, permission) => {
    mockNotifications.setPermission(permission);
    const api = fakeApi();
    await saveDevicePush(uid, stored);

    expect(await registerOnStart(uid, me)).toBe(false);

    expect(api.log).toEqual([]);
    expect(Notifications.requestPermissionsAsync).not.toHaveBeenCalled();
  });

  test('when Expo returns a different token, registers the new one and deletes the old one', async () => {
    mockNotifications.setPermission('granted');
    mockNotifications.setToken('ExponentPushToken[device-token-2]');
    const api = fakeApi();
    await saveDevicePush(uid, { enabled: true, token: TOKEN });

    await registerOnStart(uid, { pushEnabled: true });

    expect(api.log).toEqual([`POST ExponentPushToken[device-token-2]`, `DELETE /me/push-tokens/${ENCODED_TOKEN}`]);
    expect(await loadDevicePush(uid)).toEqual({ enabled: true, token: 'ExponentPushToken[device-token-2]' });
  });
});

describe('signing out', () => {
  test('deletes this device token and keeps the device switch on for the next sign-in', async () => {
    const api = fakeApi();
    await saveDevicePush(uid, { enabled: true, token: TOKEN });

    await unregisterBeforeSignOut(uid);

    expect(api.deletedPaths).toEqual([`/me/push-tokens/${ENCODED_TOKEN}`]);
    expect(await loadDevicePush(uid)).toEqual({ enabled: true, token: null });
  });

  test('sends nothing when this device has no token', async () => {
    const api = fakeApi();
    await unregisterBeforeSignOut(uid);
    expect(api.log).toEqual([]);
  });
});

describe('reminderTarget', () => {
  test('reads the household ID and name from a tapped reminder', () => {
    const response = mockNotifications.tap('household-1', { title: 'Expiring soon in Maple St. Kitchen' });
    expect(reminderTarget(response as unknown as Notifications.NotificationResponse)).toEqual({
      id: response.notification.request.identifier,
      householdId: 'household-1',
      householdName: 'Maple St. Kitchen',
    });
  });

  test('ignores notifications without a household ID, and actions other than a tap', () => {
    const response = mockNotifications.tap('household-1');
    const noData = {
      ...response,
      notification: { ...response.notification, request: { ...response.notification.request, content: { title: 'Hi', body: null, data: {} } } },
    };
    expect(reminderTarget(noData as unknown as Notifications.NotificationResponse)).toBeNull();
    expect(
      reminderTarget({ ...response, actionIdentifier: 'dismiss' } as unknown as Notifications.NotificationResponse),
    ).toBeNull();
  });
});

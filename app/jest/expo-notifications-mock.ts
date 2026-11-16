// An in-memory stand-in for expo-notifications in tests (mapped in
// jest.config.js). `mockNotifications` controls it: the permission the OS
// reports and grants, the push token Expo returns, and taps on reminders.
// jest/setup-push.js resets it before every test.

type PermissionStatus = 'granted' | 'denied' | 'undetermined';

type Permissions = {
  status: PermissionStatus;
  granted: boolean;
  canAskAgain: boolean;
  expires: 'never';
  ios?: { status: number };
};

type Listener<T> = (value: T) => void;

export const IosAuthorizationStatus = {
  NOT_DETERMINED: 0,
  DENIED: 1,
  AUTHORIZED: 2,
  PROVISIONAL: 3,
  EPHEMERAL: 4,
} as const;

export const AndroidImportance = {
  UNKNOWN: 0,
  UNSPECIFIED: 1,
  NONE: 2,
  MIN: 3,
  LOW: 4,
  DEFAULT: 5,
  HIGH: 6,
  MAX: 7,
} as const;

export const DEFAULT_ACTION_IDENTIFIER = 'expo.modules.notifications.actions.DEFAULT';

export type NotificationResponse = {
  actionIdentifier: string;
  notification: {
    date: number;
    request: {
      identifier: string;
      content: { title: string | null; body: string | null; data: Record<string, unknown> };
      trigger: unknown;
    };
  };
};

export type NotificationPermissionsStatus = Permissions;

function permissions(status: PermissionStatus, canAskAgain: boolean): Permissions {
  return { status, granted: status === 'granted', canAskAgain, expires: 'never' };
}

const state = {
  permission: permissions('undetermined', true),
  /** What the user answers when the OS asks. */
  answer: 'granted' as 'granted' | 'denied',
  token: 'ExponentPushToken[device-token-1]' as string,
  tokenError: null as Error | null,
  lastResponse: null as NotificationResponse | null,
  responseListeners: new Set<Listener<NotificationResponse>>(),
  tokenListeners: new Set<Listener<{ type: string; data: string }>>(),
};

export const getPermissionsAsync = jest.fn(async () => ({ ...state.permission }));

export const requestPermissionsAsync = jest.fn(async () => {
  if (state.permission.status === 'undetermined' || state.permission.canAskAgain) {
    state.permission = state.answer === 'granted' ? permissions('granted', true) : permissions('denied', false);
  }
  return { ...state.permission };
});

export const getExpoPushTokenAsync = jest.fn(async (_options?: { projectId?: string }) => {
  if (state.tokenError) throw state.tokenError;
  return { type: 'expo', data: state.token };
});

export const setNotificationChannelAsync = jest.fn(async (id: string, channel: { name: string }) => ({ id, ...channel }));

export const setNotificationHandler = jest.fn();

export const getLastNotificationResponse = jest.fn(() => state.lastResponse);

export const clearLastNotificationResponseAsync = jest.fn(async () => {
  state.lastResponse = null;
});

export const addNotificationResponseReceivedListener = jest.fn((listener: Listener<NotificationResponse>) => {
  state.responseListeners.add(listener);
  return { remove: () => state.responseListeners.delete(listener) };
});

export const addPushTokenListener = jest.fn((listener: Listener<{ type: string; data: string }>) => {
  state.tokenListeners.add(listener);
  return { remove: () => state.tokenListeners.delete(listener) };
});

let responseId = 0;

export const mockNotifications = {
  /** The permission the OS reports now. `blocked` is denied with no way to ask again. */
  setPermission(status: 'granted' | 'undetermined' | 'blocked' | 'deniedCanAsk') {
    state.permission =
      status === 'blocked'
        ? permissions('denied', false)
        : status === 'deniedCanAsk'
          ? permissions('denied', true)
          : permissions(status, true);
  },
  /** What the user answers when the permission prompt is shown. */
  answerPrompt(answer: 'granted' | 'denied') {
    state.answer = answer;
  },
  setToken(token: string) {
    state.token = token;
  },
  failToken(error: Error | null) {
    state.tokenError = error;
  },
  /** A reminder tapped for `householdId`. With `whileRunning`, listeners hear it too. */
  tap(householdId: string, { title = 'Expiring soon in Home', whileRunning = false } = {}) {
    responseId += 1;
    const response: NotificationResponse = {
      actionIdentifier: DEFAULT_ACTION_IDENTIFIER,
      notification: {
        date: Date.now(),
        request: { identifier: `reminder-${responseId}`, content: { title, body: 'Milk', data: { householdId } }, trigger: null },
      },
    };
    state.lastResponse = response;
    if (whileRunning) for (const listener of [...state.responseListeners]) listener(response);
    return response;
  },
  /** The OS hands the app a new push token. */
  changeToken(token: string) {
    state.token = token;
    for (const listener of [...state.tokenListeners]) listener({ type: 'ios', data: 'native-device-token' });
  },
  reset() {
    state.permission = permissions('undetermined', true);
    state.answer = 'granted';
    state.token = 'ExponentPushToken[device-token-1]';
    state.tokenError = null;
    state.lastResponse = null;
    state.responseListeners.clear();
    state.tokenListeners.clear();
    for (const fn of [
      getPermissionsAsync,
      requestPermissionsAsync,
      getExpoPushTokenAsync,
      setNotificationChannelAsync,
      setNotificationHandler,
      getLastNotificationResponse,
      clearLastNotificationResponseAsync,
      addNotificationResponseReceivedListener,
      addPushTokenListener,
    ]) {
      fn.mockClear();
    }
  },
};

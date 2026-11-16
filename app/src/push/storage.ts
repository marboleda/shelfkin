// What each user chose for this device, kept on the device: whether the
// "On this device" switch is on, and the push token last registered for them.
// The key includes the user ID, so two people sharing a phone each get their own.

import AsyncStorage from '@react-native-async-storage/async-storage';

const KEY_PREFIX = 'shelfkin.push.';

export type DevicePushState = {
  /** The "On this device" switch: the user wants reminders on this device. */
  enabled: boolean;
  /** The Expo push token last registered with the API for this user, if any. */
  token: string | null;
};

export const NO_DEVICE_PUSH: DevicePushState = { enabled: false, token: null };

export function devicePushKey(uid: string): string {
  return `${KEY_PREFIX}${uid}`;
}

function parse(raw: string | null): DevicePushState {
  if (!raw) return NO_DEVICE_PUSH;
  try {
    const value = JSON.parse(raw) as Partial<DevicePushState>;
    return {
      enabled: value.enabled === true,
      token: typeof value.token === 'string' && value.token !== '' ? value.token : null,
    };
  } catch {
    return NO_DEVICE_PUSH;
  }
}

/** The stored state, or "off, no token" when there is none or storage cannot be read. */
export async function loadDevicePush(uid: string): Promise<DevicePushState> {
  try {
    return parse(await AsyncStorage.getItem(devicePushKey(uid)));
  } catch {
    return NO_DEVICE_PUSH;
  }
}

export async function saveDevicePush(uid: string, state: DevicePushState): Promise<void> {
  try {
    await AsyncStorage.setItem(devicePushKey(uid), JSON.stringify(state));
  } catch (error) {
    console.warn('Could not save the device notification setting', error);
  }
}

/** Forgets what this user chose for this device, e.g. after their account was deleted (#46). */
export async function clearDevicePush(uid: string): Promise<void> {
  try {
    await AsyncStorage.removeItem(devicePushKey(uid));
  } catch (error) {
    console.warn('Could not clear the device notification setting', error);
  }
}

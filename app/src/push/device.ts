// The device side of push notifications: the OS permission, the Expo push
// token and the Android channel. Nothing else in the app imports
// expo-notifications for these; tests replace it with jest/expo-notifications-mock.ts.
//
// Expo push tokens exist only on iOS and Android, and only on a physical device
// (simulators and emulators without Google Play cannot receive pushes). On web
// the app shows the account switch alone.

import Constants from 'expo-constants';
import * as Device from 'expo-device';
import * as Notifications from 'expo-notifications';
import { AndroidImportance, IosAuthorizationStatus } from 'expo-notifications';
import { Linking, Platform } from 'react-native';

import type { components } from '@/api/schema';

export type PushPlatform = components['schemas']['PushPlatform'];

/**
 * The Android channel expiry reminders arrive on. The API sends pushes without
 * a `channelId`, so they go to the default channel: the config plugin in
 * app.json names this ID as the default, and the app creates it with the name
 * users see in system settings.
 */
export const ANDROID_CHANNEL_ID = 'default';
export const ANDROID_CHANNEL_NAME = 'Expiry reminders';

/**
 * Whether the OS lets the app show notifications:
 * - `granted`: yes (on iOS, provisional and ephemeral count too)
 * - `undetermined`: not decided yet, so the app may ask
 * - `blocked`: denied, and the OS will not ask again (only system settings can change it)
 */
export type PushPermission = 'granted' | 'undetermined' | 'blocked';

export type PushSetupErrorReason =
  /** The build has no EAS project ID, so Expo cannot issue a push token (#41). */
  | 'not_configured'
  /** Not a phone that can receive pushes: web, a simulator or an emulator. */
  | 'unsupported'
  /** Expo did not return a token, for example offline. */
  | 'token_failed';

export class PushSetupError extends Error {
  constructor(
    readonly reason: PushSetupErrorReason,
    message: string,
    options?: { cause?: unknown },
  ) {
    super(message, options);
    this.name = 'PushSetupError';
  }
}

/** The platform this app is running on, as the API names it, when it is a phone. */
export function nativePushPlatform(): Exclude<PushPlatform, 'web'> | null {
  return Platform.OS === 'ios' || Platform.OS === 'android' ? Platform.OS : null;
}

/** True on iOS and Android, where the "On this device" switch is shown. */
export function isNativeApp(): boolean {
  return nativePushPlatform() !== null;
}

/** True on a physical iOS or Android device, which can get an Expo push token. */
export function canReceivePush(): boolean {
  return isNativeApp() && Device.isDevice;
}

function toPermission(status: Notifications.NotificationPermissionsStatus): PushPermission {
  const iosStatus = status.ios?.status;
  if (
    status.granted ||
    iosStatus === IosAuthorizationStatus.PROVISIONAL ||
    iosStatus === IosAuthorizationStatus.EPHEMERAL
  ) {
    return 'granted';
  }
  // Android 13+ may let the app ask again after one denial; iOS never does.
  if (status.status === 'undetermined' || status.canAskAgain) return 'undetermined';
  return 'blocked';
}

/** The current OS permission, without asking. */
export async function getPushPermission(): Promise<PushPermission> {
  if (!isNativeApp()) return 'blocked';
  return toPermission(await Notifications.getPermissionsAsync());
}

/**
 * Asks for the OS permission when it is not decided yet, and returns the
 * result. Never shows a prompt when it is already granted or blocked.
 */
export async function requestPushPermission(): Promise<PushPermission> {
  const current = await getPushPermission();
  if (current !== 'undetermined') return current;
  // Android 13+ shows the prompt only once a channel exists.
  await ensureAndroidChannel();
  return toPermission(await Notifications.requestPermissionsAsync());
}

/** Creates the "Expiry reminders" channel on Android. Safe to call more than once. */
export async function ensureAndroidChannel(): Promise<void> {
  if (Platform.OS !== 'android') return;
  await Notifications.setNotificationChannelAsync(ANDROID_CHANNEL_ID, {
    name: ANDROID_CHANNEL_NAME,
    importance: AndroidImportance.HIGH,
  });
}

/**
 * The EAS project ID Expo needs to issue a push token, from the build's
 * config (`extra.eas.projectId` in app.json, set up in #41). Null when the
 * build has none.
 */
export function easProjectId(): string | null {
  const fromEas = Constants.easConfig?.projectId;
  const fromExtra = (Constants.expoConfig?.extra as { eas?: { projectId?: unknown } } | undefined)?.eas?.projectId;
  const id = fromEas ?? fromExtra;
  return typeof id === 'string' && id.trim() !== '' ? id : null;
}

/** This device's Expo push token. Throws a `PushSetupError` when there is none to get. */
export async function getPushToken(): Promise<string> {
  if (!canReceivePush()) {
    throw new PushSetupError('unsupported', 'Push notifications need a physical iOS or Android device');
  }
  const projectId = easProjectId();
  if (!projectId) {
    throw new PushSetupError('not_configured', 'This build has no EAS project ID, so it cannot get a push token');
  }
  try {
    const { data } = await Notifications.getExpoPushTokenAsync({ projectId });
    return data;
  } catch (cause) {
    throw new PushSetupError('token_failed', 'Could not get an Expo push token', { cause });
  }
}

/** Opens this app's page in the system settings, where notifications can be turned back on. */
export function openSystemSettings(): Promise<void> {
  return Linking.openSettings();
}

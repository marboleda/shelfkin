// Turning expiry reminders on and off, and keeping this device's push token
// registered with the API (#21, decision in #48). Two switches:
//
// - The account switch is `pushEnabled` on the user (`PATCH /me`). It covers
//   all their devices. Turning it off keeps their tokens, so turning it on
//   again needs no re-registration on other devices.
// - The device switch is whether this device's token is registered
//   (`POST` / `DELETE /me/push-tokens`). Its state is kept per user on the device (storage.ts).
//
// The functions here run one at a time, so a start-up registration and a tap
// on a switch never interleave.

import type { QueryClient } from '@tanstack/react-query';

import { apiClient, callApi } from '@/api/client';
import { queryKeys } from '@/api/query-keys';
import type { components } from '@/api/schema';

import {
  canReceivePush,
  getPushPermission,
  getPushToken,
  nativePushPlatform,
  PushSetupError,
  requestPushPermission,
  type PushSetupErrorReason,
} from './device';
import { loadDevicePush, saveDevicePush } from './storage';

type Me = components['schemas']['User'];

/** How long sign-out waits for the token to be deleted before signing out anyway. */
export const SIGN_OUT_DELETE_TIMEOUT_MS = 5000;

export type TurnOnResult =
  | { ok: true }
  /** The OS permission is denied: nothing was sent to the API. */
  | { ok: false; reason: 'blocked' }
  /** No push token could be got (see `PushSetupErrorReason`): nothing was sent to the API. */
  | { ok: false; reason: PushSetupErrorReason };

let queue: Promise<unknown> = Promise.resolve();

/** Runs `task` after every earlier one has finished. */
function serialized<T>(task: () => Promise<T>): Promise<T> {
  const run = queue.then(task, task);
  queue = run.catch(() => {});
  return run;
}

/** `POST /me/push-tokens`: registers or refreshes the token for the signed-in user. */
export async function registerPushToken(token: string): Promise<void> {
  const platform = nativePushPlatform();
  if (!platform) throw new PushSetupError('unsupported', 'Only phones register push tokens');
  await callApi(() => apiClient.POST('/me/push-tokens', { body: { token, platform } }));
}

/**
 * `DELETE /me/push-tokens/{token}`. openapi-fetch URL-encodes the path
 * parameter, so `[` and `]` in the token are sent as `%5B` and `%5D`.
 */
export async function deletePushToken(token: string, signal?: AbortSignal): Promise<void> {
  await callApi(() => apiClient.DELETE('/me/push-tokens/{token}', { params: { path: { token } }, signal }));
}

async function setPushEnabled(queryClient: QueryClient, pushEnabled: boolean): Promise<Me> {
  const me = await callApi(() => apiClient.PATCH('/me', { body: { pushEnabled } }));
  queryClient.setQueryData(queryKeys.me(), me);
  return me;
}

/** Deletes a token this device no longer uses. A failure only leaves a stale token, which #47 prunes. */
async function forgetOldToken(oldToken: string | null, newToken: string): Promise<void> {
  if (!oldToken || oldToken === newToken) return;
  try {
    await deletePushToken(oldToken);
  } catch (error) {
    console.warn('Could not delete the previous push token', error);
  }
}

/** Asks for permission if needed, gets the token, registers it and remembers it. */
async function registerThisDevice(uid: string): Promise<TurnOnResult> {
  const permission = await requestPushPermission();
  if (permission !== 'granted') return { ok: false, reason: 'blocked' };
  let token: string;
  try {
    token = await getPushToken();
  } catch (error) {
    if (error instanceof PushSetupError) return { ok: false, reason: error.reason };
    throw error;
  }
  const previous = await loadDevicePush(uid);
  await registerPushToken(token);
  await saveDevicePush(uid, { enabled: true, token });
  await forgetOldToken(previous.token, token);
  return { ok: true };
}

/**
 * Turns the account switch on. On a phone that can receive pushes, this also
 * turns this device on: it asks for the OS permission (when not decided yet),
 * and only when granted registers the token and sets `pushEnabled: true`. On
 * web, or a simulator, it only sets `pushEnabled`. Throws an `ApiError` when a request fails.
 */
export function turnOnAccount(queryClient: QueryClient, uid: string): Promise<TurnOnResult> {
  return serialized(async () => {
    if (canReceivePush()) {
      const result = await registerThisDevice(uid);
      if (!result.ok) return result;
    }
    await setPushEnabled(queryClient, true);
    return { ok: true } as const;
  });
}

/** Turns the account switch off: `pushEnabled: false`. Every device keeps its token (#21). */
export function turnOffAccount(queryClient: QueryClient): Promise<void> {
  return serialized(async () => {
    await setPushEnabled(queryClient, false);
  });
}

/** Turns "On this device" on: the same permission and registration as the account switch. */
export function turnOnDevice(uid: string): Promise<TurnOnResult> {
  return serialized(() => registerThisDevice(uid));
}

/**
 * Turns "On this device" off: deletes this device's token. The account switch
 * stays on for the user's other devices. Throws an `ApiError` when the delete fails.
 */
export function turnOffDevice(uid: string): Promise<void> {
  return serialized(async () => {
    const { token } = await loadDevicePush(uid);
    if (token) await deletePushToken(token);
    await saveDevicePush(uid, { enabled: false, token: null });
  });
}

/**
 * Runs on each app start while signed in. When the account switch is on, the
 * device switch is on and the permission is granted, registers the token
 * again: this refreshes its `last_seen_at`, and moves it to this user if
 * someone else used the device before. If Expo returns a different token than
 * before, the new one is registered and the old one deleted.
 *
 * Returns whether it registered.
 */
export function registerOnStart(uid: string, me: Pick<Me, 'pushEnabled'>): Promise<boolean> {
  return serialized(async () => {
    if (!me.pushEnabled || !canReceivePush()) return false;
    const stored = await loadDevicePush(uid);
    if (!stored.enabled) return false;
    if ((await getPushPermission()) !== 'granted') return false;
    const token = await getPushToken();
    await registerPushToken(token);
    await saveDevicePush(uid, { enabled: true, token });
    await forgetOldToken(stored.token, token);
    return true;
  });
}

/**
 * Runs before signing out of Firebase (the delete needs the ID token): deletes
 * this device's token so the next person to use the phone gets none of this
 * user's reminders. The device switch stays on, so signing in again registers
 * the token again. Gives up after `SIGN_OUT_DELETE_TIMEOUT_MS`, for example
 * offline; the caller signs out anyway.
 */
export async function unregisterBeforeSignOut(uid: string): Promise<void> {
  const stored = await loadDevicePush(uid);
  if (!stored.token) return;
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), SIGN_OUT_DELETE_TIMEOUT_MS);
  try {
    await deletePushToken(stored.token, controller.signal);
  } finally {
    clearTimeout(timer);
  }
  await saveDevicePush(uid, { enabled: stored.enabled, token: null });
}

/** Test only: forgets queued work from an earlier test. */
export function resetPushQueueForTests() {
  queue = Promise.resolve();
}

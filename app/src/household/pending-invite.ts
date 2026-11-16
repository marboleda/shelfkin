// The invite code from a link opened while signed out (#33), kept on the device
// until the user has signed in (or signed up) and the join has finished.
//
// It survives switching between /sign-in and /sign-up, a reload on web and an
// app restart on mobile. Opening another invite link replaces it. It is
// cleared when the join finishes (success or a definite error) and on every
// sign-out, so a later sign-in never joins a household unexpectedly.

import AsyncStorage from '@react-native-async-storage/async-storage';
import { useSyncExternalStore } from 'react';

import { onSignedOut } from '@/auth/session';

export const PENDING_INVITE_KEY = 'shelfkin.pendingInvite';

/** Shown on /sign-in and /sign-up while an invite is waiting. */
export const PENDING_INVITE_MESSAGE = 'Sign in or create an account to join the household';

/** `undefined` until the device storage has been read. */
type Snapshot = string | null | undefined;

let snapshot: Snapshot = undefined;
let loading: Promise<string | null> | null = null;
// Bumped by every save or clear, so a slow first read never overwrites them.
let version = 0;
const subscribers = new Set<() => void>();

function set(next: string | null) {
  version += 1;
  snapshot = next;
  for (const subscriber of [...subscribers]) subscriber();
}

/** The pending code, read from the device the first time. */
export function loadPendingInvite(): Promise<string | null> {
  if (snapshot !== undefined) return Promise.resolve(snapshot);
  if (!loading) {
    const startedAt = version;
    const read: Promise<string | null> = AsyncStorage.getItem(PENDING_INVITE_KEY)
      .catch(() => null)
      .then((stored) => {
        if (loading === read) loading = null;
        if (version === startedAt) set(stored || null);
        return snapshot ?? null;
      });
    loading = read;
  }
  return loading;
}

/** Remembers `code`, replacing any earlier one. The in-memory value changes at once. */
export function savePendingInvite(code: string): Promise<void> {
  set(code);
  return AsyncStorage.setItem(PENDING_INVITE_KEY, code).catch((error) => {
    console.warn('Could not remember the invite code', error);
  });
}

export function clearPendingInvite(): Promise<void> {
  set(null);
  return AsyncStorage.removeItem(PENDING_INVITE_KEY).catch((error) => {
    console.warn('Could not forget the invite code', error);
  });
}

function subscribe(callback: () => void) {
  subscribers.add(callback);
  void loadPendingInvite();
  return () => {
    subscribers.delete(callback);
  };
}

function getSnapshot() {
  return snapshot;
}

/** The pending code: `undefined` while it is read from the device, `null` when there is none. */
export function usePendingInvite(): Snapshot {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

// Every sign-out, including one in another browser tab or an ended session.
onSignedOut(() => clearPendingInvite());

/** Test only: forgets the in-memory value, as if the app had just started. */
export function resetPendingInviteForTests() {
  snapshot = undefined;
  loading = null;
  version += 1;
}

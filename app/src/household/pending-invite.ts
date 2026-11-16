// The invite code from a link opened while signed out (#33), kept on the device
// until the user has signed in (or signed up) and the join has finished.
//
// It survives switching between /sign-in and /sign-up, a reload on web and an
// app restart on mobile. Opening another invite link replaces it. It is
// cleared when the join finishes (success or a definite error) and on every
// sign-out, so a later sign-in never joins a household unexpectedly.
//
// One exception (#107): when a join gets a 401 and that ends the session, the
// join screen saves its code with `keepOnEndedSession`, and the sign-out of
// that ended session keeps it, so signing in again finishes the join. The
// result is the same whichever runs first, the save or the sign-out listener:
// a listener that runs first clears, and the save then stores the code again;
// a save that runs first is left alone by the listener.
//
// The mark is used at most once: the sign-out that keeps the code consumes it.
// It is also dropped when a join starts or the join screen closes while the
// user is still signed in (`releaseKeptPendingInvite`), when the join
// finishes, when another code is saved, and on any other sign-out. So a later,
// unrelated ended session clears the code as usual.

import AsyncStorage from '@react-native-async-storage/async-storage';
import { useSyncExternalStore } from 'react';

import { getAuthState, onSignedOut } from '@/auth/session';

export const PENDING_INVITE_KEY = 'shelfkin.pendingInvite';

/** Shown on /sign-in and /sign-up while an invite is waiting. */
export const PENDING_INVITE_MESSAGE = 'Sign in or create an account to join the household';

/** `undefined` until the device storage has been read. */
type Snapshot = string | null | undefined;

let snapshot: Snapshot = undefined;
let loading: Promise<string | null> | null = null;
// Bumped by every save or clear, so a slow first read never overwrites them.
let version = 0;
// The code of a join that got a 401, kept through an ended session's sign-out.
let keptCode: string | null = null;
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

type SaveOptions = {
  /**
   * Keep this code through the sign-out of the ended session that a join's 401
   * caused (see the top of the file). After a plain sign-out (not an ended
   * session) nothing is saved.
   */
  keepOnEndedSession?: boolean;
};

/** Remembers `code`, replacing any earlier one. The in-memory value changes at once. */
export function savePendingInvite(code: string, options: SaveOptions = {}): Promise<void> {
  if (options.keepOnEndedSession) {
    const auth = getAuthState();
    if (auth.status === 'signedOut' && !auth.sessionEnded) return Promise.resolve();
    keptCode = code;
  } else if (keptCode !== code) {
    keptCode = null;
  }
  set(code);
  return AsyncStorage.setItem(PENDING_INVITE_KEY, code).catch((error) => {
    console.warn('Could not remember the invite code', error);
  });
}

export function clearPendingInvite(): Promise<void> {
  keptCode = null;
  set(null);
  return AsyncStorage.removeItem(PENDING_INVITE_KEY).catch((error) => {
    console.warn('Could not forget the invite code', error);
  });
}

/**
 * Drops the `keepOnEndedSession` mark while the user is still signed in: a new
 * join has started, or the join screen closed. After an ended session's
 * sign-out it does nothing, so the code of the join that got the 401 survives.
 */
export function releaseKeptPendingInvite(): void {
  if (getAuthState().status === 'signedIn') keptCode = null;
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

// Every sign-out, including one in another browser tab or an ended session,
// except the ended session of a join that got a 401 (see the top of the file).
onSignedOut(() => {
  const auth = getAuthState();
  const ended = auth.status === 'signedOut' && auth.sessionEnded;
  if (ended && keptCode !== null && snapshot === keptCode) {
    // Used once: a later ended session clears the code.
    keptCode = null;
    return;
  }
  return clearPendingInvite();
});

/** Test only: forgets the in-memory value, as if the app had just started. */
export function resetPendingInviteForTests() {
  snapshot = undefined;
  loading = null;
  keptCode = null;
  version += 1;
}

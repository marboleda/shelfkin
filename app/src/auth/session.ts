// The app's one source of auth state: who is signed in, and the sign-in,
// sign-up, password reset and sign-out actions. Screens read the state with
// `useAuth()`; nothing else talks to Firebase Auth.
//
// Sign-out hook points (used by #34, #35 and #46):
// - `addBeforeSignOutTask(task)` runs `task` before Firebase signs out, while
//   the user's ID token still works (e.g. to remove the push token).
// - `onSignedOut(listener)` runs after the user is signed out, however that
//   happened: the Sign out button, a session that ended, or another browser tab.

import {
  createUserWithEmailAndPassword,
  onAuthStateChanged,
  sendPasswordResetEmail,
  signInWithEmailAndPassword,
  signOut as firebaseSignOut,
  type User,
} from 'firebase/auth';
import { useSyncExternalStore } from 'react';

import { firebaseConfig } from './config';
import { getFirebaseAuth } from './firebase';
import { clearPendingProfile, savePendingProfile } from './pending-profile';

export type AuthUser = {
  uid: string;
  email: string | null;
};

export type AuthState =
  | { status: 'loading' }
  | { status: 'signedOut'; /** True when the user was signed in earlier in this app run. */ signedOutInSession: boolean; sessionEnded: boolean }
  | { status: 'signedIn'; user: AuthUser };

type Task = () => void | Promise<void>;

let snapshot: AuthState = { status: 'loading' };
let started = false;
let wasSignedIn = false;
let sessionEnded = false;
let signOutInFlight: Promise<void> | null = null;
const subscribers = new Set<() => void>();
const beforeSignOutTasks = new Set<Task>();
const signedOutListeners = new Set<Task>();

function emit() {
  for (const subscriber of [...subscribers]) subscriber();
}

function setSnapshot(next: AuthState) {
  snapshot = next;
  emit();
}

function handleUser(user: User | null) {
  const previous = snapshot;
  if (user) {
    if (previous.status === 'signedIn' && previous.user.uid === user.uid && previous.user.email === user.email) {
      return;
    }
    wasSignedIn = true;
    sessionEnded = false;
    setSnapshot({ status: 'signedIn', user: { uid: user.uid, email: user.email } });
  } else {
    setSnapshot({ status: 'signedOut', signedOutInSession: wasSignedIn, sessionEnded });
  }
  if (previous.status === 'signedIn' && (!user || user.uid !== previous.user.uid)) {
    for (const listener of [...signedOutListeners]) {
      Promise.resolve()
        .then(listener)
        .catch((error) => console.warn('A signed-out listener failed', error));
    }
  }
}

/** Starts listening to Firebase on first use. Does nothing when the build has no Firebase config. */
function start() {
  if (started || !firebaseConfig.ok) return;
  started = true;
  // Firebase restores the persisted session first, then calls back once with the
  // user (or null). Until then the state stays `loading`.
  onAuthStateChanged(getFirebaseAuth(), handleUser);
}

function subscribe(callback: () => void) {
  start();
  subscribers.add(callback);
  return () => {
    subscribers.delete(callback);
  };
}

function getSnapshot() {
  return snapshot;
}

/** The current auth state. `loading` while Firebase restores the session at app start. */
export function useAuth(): AuthState {
  return useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}

export function getAuthState(): AuthState {
  start();
  return snapshot;
}

/** Registers a task to run before every sign-out. Returns a function that removes it. */
export function addBeforeSignOutTask(task: Task): () => void {
  beforeSignOutTasks.add(task);
  return () => {
    beforeSignOutTasks.delete(task);
  };
}

/** Registers a listener for every sign-out, including one in another browser tab. Returns a function that removes it. */
export function onSignedOut(listener: Task): () => void {
  signedOutListeners.add(listener);
  return () => {
    signedOutListeners.delete(listener);
  };
}

/**
 * The signed-in user's Firebase ID token, or null when nobody is signed in.
 * Firebase returns a cached token and refreshes it shortly before it expires;
 * `forceRefresh` gets a new one now.
 */
export async function getIdToken(forceRefresh = false): Promise<string | null> {
  if (!firebaseConfig.ok) return null;
  const user = getFirebaseAuth().currentUser;
  return user ? user.getIdToken(forceRefresh) : null;
}

export async function signIn(email: string, password: string): Promise<void> {
  await signInWithEmailAndPassword(getFirebaseAuth(), email.trim(), password);
}

/**
 * Creates the account and signs in. The name is kept on the device first, so
 * the profile sync sends it with `PATCH /me` once signed in, and again on later
 * starts until the API has it.
 */
export async function signUp(name: string, email: string, password: string): Promise<void> {
  const trimmedEmail = email.trim();
  await savePendingProfile({ email: trimmedEmail, displayName: name.trim() });
  try {
    await createUserWithEmailAndPassword(getFirebaseAuth(), trimmedEmail, password);
  } catch (error) {
    await clearPendingProfile();
    throw error;
  }
}

export async function sendPasswordReset(email: string): Promise<void> {
  try {
    await sendPasswordResetEmail(getFirebaseAuth(), email.trim());
  } catch (error) {
    // Projects without email enumeration protection (like the emulator) report
    // unknown emails. The screen says the same thing either way.
    if ((error as { code?: unknown })?.code === 'auth/user-not-found') return;
    throw error;
  }
}

/**
 * Runs the before-sign-out tasks, then signs out of Firebase. The layouts then
 * send the user to /sign-in, and the `onSignedOut` listeners clear cached data.
 * A task that fails does not stop the sign-out.
 */
export function signOut(options: { sessionEnded?: boolean } = {}): Promise<void> {
  if (options.sessionEnded) sessionEnded = true;
  if (!signOutInFlight) {
    signOutInFlight = (async () => {
      try {
        const results = await Promise.allSettled([...beforeSignOutTasks].map((task) => task()));
        for (const result of results) {
          if (result.status === 'rejected') console.warn('A before-sign-out task failed', result.reason);
        }
        await firebaseSignOut(getFirebaseAuth());
      } finally {
        signOutInFlight = null;
      }
    })();
  }
  return signOutInFlight;
}

/** Test only: forgets everything this module remembers about earlier sign-ins in this run. */
export function resetAuthSessionForTests() {
  wasSignedIn = false;
  sessionEnded = false;
  signOutInFlight = null;
  beforeSignOutTasks.clear();
}

/** Signs out after the API rejected a fresh token. /sign-in then says the session ended. */
export function endSession(): Promise<void> {
  return signOut({ sessionEnded: true }).catch((error) => {
    console.warn('Sign-out after an ended session failed', error);
  });
}

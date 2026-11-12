// An in-memory stand-in for `firebase/auth`, used by every test (see the
// moduleNameMapper in jest.config.js). It implements just the calls the app
// makes, keeps accounts in memory, and exports `mockFirebase` so tests can
// create accounts, sign a user in, make the next call fail or hold the
// session restore at app start.

type Listener = (user: FakeUser | null) => void;

export class FakeUser {
  readonly uid: string;
  readonly email: string;
  /** Bumped by every forced refresh, so tests can tell tokens apart. */
  tokenVersion = 1;
  readonly getIdToken = jest.fn(async (forceRefresh?: boolean) => {
    if (forceRefresh) this.tokenVersion += 1;
    return `token-${this.uid}-${this.tokenVersion}`;
  });

  constructor(uid: string, email: string) {
    this.uid = uid;
    this.email = email;
  }
}

function authError(code: string): Error & { code: string } {
  const error = new Error(`Firebase: Error (${code}).`) as Error & { code: string };
  error.code = code;
  return error;
}

const state = {
  accounts: new Map<string, { password: string; user: FakeUser }>(),
  currentUser: null as FakeUser | null,
  listeners: new Set<Listener>(),
  /** Whether the session restore at start has finished. */
  restored: false,
  holdRestore: false,
  nextError: null as string | null,
  nextUid: 1,
  resetEmails: [] as string[],
};

const fakeAuth = {
  get currentUser() {
    return state.restored ? state.currentUser : null;
  },
};

function notify() {
  for (const listener of [...state.listeners]) listener(state.currentUser);
}

function finishRestore() {
  if (state.restored) return;
  state.restored = true;
  notify();
}

async function failIfAsked() {
  // Settle on a later microtask, like a real network call.
  await Promise.resolve();
  if (state.nextError) {
    const code = state.nextError;
    state.nextError = null;
    throw authError(code);
  }
}

function setCurrentUser(user: FakeUser | null) {
  state.currentUser = user;
  if (state.restored) notify();
}

export const initializeAuth = jest.fn(() => fakeAuth);
export const getAuth = jest.fn(() => fakeAuth);
export const connectAuthEmulator = jest.fn();
export const getReactNativePersistence = jest.fn((storage: unknown) => ({ type: 'react-native', storage }));
export const browserLocalPersistence = { type: 'browser-local' };

export const onAuthStateChanged = jest.fn((_auth: unknown, listener: Listener) => {
  state.listeners.add(listener);
  if (state.restored) {
    queueMicrotask(() => {
      if (state.listeners.has(listener)) listener(state.currentUser);
    });
  } else if (!state.holdRestore) {
    queueMicrotask(finishRestore);
  }
  return () => {
    state.listeners.delete(listener);
  };
});

export const signInWithEmailAndPassword = jest.fn(async (_auth: unknown, email: string, password: string) => {
  await failIfAsked();
  const account = state.accounts.get(email.toLowerCase());
  if (!account || account.password !== password) throw authError('auth/invalid-credential');
  setCurrentUser(account.user);
  return { user: account.user };
});

export const createUserWithEmailAndPassword = jest.fn(
  async (_auth: unknown, email: string, password: string) => {
    await failIfAsked();
    if (state.accounts.has(email.toLowerCase())) throw authError('auth/email-already-in-use');
    const user = mockFirebase.addAccount(email, password);
    setCurrentUser(user);
    return { user };
  },
);

export const sendPasswordResetEmail = jest.fn(async (_auth: unknown, email: string) => {
  await failIfAsked();
  state.resetEmails.push(email);
});

export const signOut = jest.fn(async () => {
  await failIfAsked();
  setCurrentUser(null);
});

/** Test controls. */
export const mockFirebase = {
  /** Clears accounts, signs out and lets the next session restore finish at once. */
  reset() {
    state.accounts.clear();
    state.nextError = null;
    state.holdRestore = false;
    state.resetEmails = [];
    state.currentUser = null;
    state.restored = true;
    notify();
  },
  addAccount(email: string, password: string): FakeUser {
    const user = new FakeUser(`uid-${state.nextUid++}`, email);
    state.accounts.set(email.toLowerCase(), { password, user });
    return user;
  },
  /** Signs a user in, as if their session was restored from storage. */
  signIn(email = 'sam@example.com', password = 'correct-horse'): FakeUser {
    const user = state.accounts.get(email.toLowerCase())?.user ?? mockFirebase.addAccount(email, password);
    setCurrentUser(user);
    return user;
  },
  /** Signs out without going through the app, as another browser tab would. */
  signOutElsewhere() {
    setCurrentUser(null);
  },
  /** Makes the next sign-in, sign-up, reset or sign-out call fail with this code. */
  failNext(code: string) {
    state.nextError = code;
  },
  /** Starts the next app run with the session restore still pending, until `finishRestore()`. */
  holdRestore() {
    state.restored = false;
    state.holdRestore = true;
  },
  finishRestore() {
    state.holdRestore = false;
    finishRestore();
  },
  get currentUser() {
    return state.currentUser;
  },
  get resetEmails() {
    return state.resetEmails;
  },
};

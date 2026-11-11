// Firebase Auth on web. The session is kept in the browser's local storage, so
// reloading the tab keeps the user signed in, and signing in or out in one tab
// reaches the other open tabs. iOS and Android use firebase.native.ts.

import { initializeApp } from 'firebase/app';
import { browserLocalPersistence, connectAuthEmulator, initializeAuth, type Auth } from 'firebase/auth';

import { firebaseConfig } from './config';

let auth: Auth | null = null;

/** The app's Firebase Auth instance, created on first use. Only call it when `firebaseConfig.ok`. */
export function getFirebaseAuth(): Auth {
  if (auth) return auth;
  if (!firebaseConfig.ok) throw new Error(firebaseConfig.message);
  const { emulatorHost, ...options } = firebaseConfig.settings;
  const app = initializeApp(options);
  auth = initializeAuth(app, { persistence: browserLocalPersistence });
  if (emulatorHost) {
    connectAuthEmulator(auth, `http://${emulatorHost}`, { disableWarnings: true });
  }
  return auth;
}

// Firebase Auth on iOS and Android. The session is kept in AsyncStorage, so
// closing and reopening the app keeps the user signed in. Web uses firebase.ts.

import AsyncStorage from '@react-native-async-storage/async-storage';
import { initializeApp } from 'firebase/app';
import * as firebaseAuth from 'firebase/auth';
import { connectAuthEmulator, initializeAuth, type Auth, type Persistence } from 'firebase/auth';

import { firebaseConfig } from './config';

// Metro bundles Firebase's React Native build on iOS and Android, which exports
// getReactNativePersistence. TypeScript only sees the browser typings (the
// `types` condition comes first in @firebase/auth's exports), so declare it here.
const { getReactNativePersistence } = firebaseAuth as unknown as {
  getReactNativePersistence: (storage: typeof AsyncStorage) => Persistence;
};

let auth: Auth | null = null;

/** The app's Firebase Auth instance, created on first use. Only call it when `firebaseConfig.ok`. */
export function getFirebaseAuth(): Auth {
  if (auth) return auth;
  if (!firebaseConfig.ok) throw new Error(firebaseConfig.message);
  const { emulatorHost, ...options } = firebaseConfig.settings;
  const app = initializeApp(options);
  auth = initializeAuth(app, { persistence: getReactNativePersistence(AsyncStorage) });
  if (emulatorHost) {
    connectAuthEmulator(auth, `http://${emulatorHost}`, { disableWarnings: true });
  }
  return auth;
}

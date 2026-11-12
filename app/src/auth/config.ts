// Which Firebase project the app signs in with. See app/README.md ("Sign-in and
// the Auth emulator") for how to set it.

/** The Auth emulator's project in compose.yaml. The `demo-` prefix means it can never reach a real project. */
export const DEMO_PROJECT_ID = 'demo-shelfkin';

export type FirebaseSettings = {
  apiKey: string;
  authDomain: string;
  projectId: string;
  /** `host:port` of the Firebase Auth emulator, when the app should use it. */
  emulatorHost?: string;
};

export type FirebaseConfig = { ok: true; settings: FirebaseSettings } | { ok: false; message: string };

export type FirebaseEnv = {
  apiKey?: string;
  authDomain?: string;
  projectId?: string;
  emulatorHost?: string;
};

function clean(value: string | undefined): string {
  return value?.trim() ?? '';
}

/**
 * Picks the Firebase settings from the `EXPO_PUBLIC_FIREBASE_*` variables.
 *
 * - Development: anything unset falls back to the `demo-shelfkin` emulator
 *   project, and the app uses the emulator when its host is set.
 * - Production build: all three of API key, auth domain and project ID are
 *   required, and the emulator variable must not be set. Otherwise the app
 *   shows a configuration error instead of talking to the wrong project.
 */
export function resolveFirebaseConfig(env: FirebaseEnv, isDev: boolean): FirebaseConfig {
  const apiKey = clean(env.apiKey);
  const authDomain = clean(env.authDomain);
  const projectId = clean(env.projectId);
  const emulatorHost = clean(env.emulatorHost).replace(/^https?:\/\//, '').replace(/\/+$/, '');

  if (!isDev) {
    if (emulatorHost) {
      return {
        ok: false,
        message:
          'This build is set to use the Firebase Auth emulator. Unset EXPO_PUBLIC_FIREBASE_AUTH_EMULATOR_HOST for production builds (see app/.env.example).',
      };
    }
    const missing = [
      ['EXPO_PUBLIC_FIREBASE_API_KEY', apiKey],
      ['EXPO_PUBLIC_FIREBASE_AUTH_DOMAIN', authDomain],
      ['EXPO_PUBLIC_FIREBASE_PROJECT_ID', projectId],
    ]
      .filter(([, value]) => !value)
      .map(([name]) => name);
    if (missing.length > 0) {
      return {
        ok: false,
        message: `This build has no sign-in settings. Set ${missing.join(', ')} when building the app (see app/.env.example).`,
      };
    }
    return { ok: true, settings: { apiKey, authDomain, projectId } };
  }

  const resolvedProject = projectId || DEMO_PROJECT_ID;
  return {
    ok: true,
    settings: {
      // The emulator accepts any API key.
      apiKey: apiKey || 'demo-api-key',
      authDomain: authDomain || `${resolvedProject}.firebaseapp.com`,
      projectId: resolvedProject,
      ...(emulatorHost ? { emulatorHost } : {}),
    },
  };
}

// Each variable must be written out in full (dot notation) so Expo can inline it at build time.
export const firebaseConfig: FirebaseConfig = resolveFirebaseConfig(
  {
    apiKey: process.env.EXPO_PUBLIC_FIREBASE_API_KEY,
    authDomain: process.env.EXPO_PUBLIC_FIREBASE_AUTH_DOMAIN,
    projectId: process.env.EXPO_PUBLIC_FIREBASE_PROJECT_ID,
    emulatorHost: process.env.EXPO_PUBLIC_FIREBASE_AUTH_EMULATOR_HOST,
  },
  __DEV__,
);

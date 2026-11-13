import { DEMO_PROJECT_ID, resolveFirebaseConfig } from '@/auth/config';
import { AUTH_MESSAGES, authErrorMessage, resetSentMessage } from '@/auth/errors';
import { redirectTarget, signInHref } from '@/auth/redirect';
import { validateEmail, validateName, validateNewPassword } from '@/auth/validation';

const FULL_ENV = {
  apiKey: 'AIza-test',
  authDomain: 'shelfkin.firebaseapp.com',
  projectId: 'shelfkin-prod',
};

describe('Firebase config', () => {
  test('development with nothing set uses the demo-shelfkin project and no emulator', () => {
    const config = resolveFirebaseConfig({}, true);
    expect(config).toEqual({
      ok: true,
      settings: { apiKey: 'demo-api-key', authDomain: 'demo-shelfkin.firebaseapp.com', projectId: DEMO_PROJECT_ID },
    });
    expect(DEMO_PROJECT_ID).toBe('demo-shelfkin');
  });

  test('development connects to the emulator when its host is set', () => {
    const config = resolveFirebaseConfig({ projectId: 'demo-shelfkin', emulatorHost: 'http://10.0.2.2:9099/' }, true);
    expect(config.ok && config.settings.emulatorHost).toBe('10.0.2.2:9099');
  });

  test('a production build with all three values uses them', () => {
    expect(resolveFirebaseConfig(FULL_ENV, false)).toEqual({ ok: true, settings: FULL_ENV });
  });

  test.each(['apiKey', 'authDomain', 'projectId'] as const)(
    'a production build without %s is a configuration error',
    (missing) => {
      const config = resolveFirebaseConfig({ ...FULL_ENV, [missing]: '  ' }, false);
      expect(config.ok).toBe(false);
      expect(!config.ok && config.message).toMatch(/EXPO_PUBLIC_FIREBASE_/);
    },
  );

  test('a production build with the emulator variable set is a configuration error', () => {
    const config = resolveFirebaseConfig({ ...FULL_ENV, emulatorHost: 'localhost:9099' }, false);
    expect(config.ok).toBe(false);
    expect(!config.ok && config.message).toMatch(/EXPO_PUBLIC_FIREBASE_AUTH_EMULATOR_HOST/);
  });
});

describe('validation', () => {
  test.each([
    ['', 'Enter your name'],
    ['   ', 'Enter your name'],
    ['a'.repeat(51), 'Use 50 characters or fewer'],
  ])('name %j is invalid', (name, message) => {
    expect(validateName(name)).toBe(message);
  });

  test.each(['Sam', `  ${'a'.repeat(50)}  `, '😀'.repeat(50)])('name %j is valid (trimmed, 1 to 50 characters)', (name) => {
    expect(validateName(name)).toBeNull();
  });

  test.each(['', 'sam', 'sam@', 'sam@example', 'sam @example.com'])('email %j is invalid', (email) => {
    expect(validateEmail(email)).not.toBeNull();
  });

  test.each(['sam@example.com', ' sam@example.co.uk '])('email %j is valid', (email) => {
    expect(validateEmail(email)).toBeNull();
  });

  test('a new password needs at least 8 characters', () => {
    expect(validateNewPassword('')).toBe('Enter a password');
    expect(validateNewPassword('1234567')).toBe('Use at least 8 characters');
    expect(validateNewPassword('12345678')).toBeNull();
  });
});

describe('Firebase error messages', () => {
  const error = (code: string) => Object.assign(new Error(`Firebase: Error (${code}).`), { code });

  test.each(['auth/invalid-credential', 'auth/wrong-password', 'auth/user-not-found', 'auth/user-disabled'])(
    '%s says the email or password is incorrect',
    (code) => {
      expect(authErrorMessage(error(code))).toBe('Email or password is incorrect');
    },
  );

  test('too many attempts, no network and the email in use each have their own message', () => {
    expect(authErrorMessage(error('auth/too-many-requests'))).toBe(AUTH_MESSAGES.tooManyRequests);
    expect(authErrorMessage(error('auth/network-request-failed'))).toBe(AUTH_MESSAGES.network);
    expect(authErrorMessage(error('auth/email-already-in-use'))).toBe('An account with this email already exists');
  });

  test.each([error('auth/internal-error'), error('auth/something-new'), new Error('boom'), 'text', null])(
    'anything else gets the generic message, never a Firebase code (%#)',
    (value) => {
      const message = authErrorMessage(value);
      expect(message).toBe(AUTH_MESSAGES.generic);
      expect(message).not.toMatch(/auth\//);
    },
  );

  test('the reset message names the email', () => {
    expect(resetSentMessage('sam@example.com')).toBe(
      "If an account exists for sam@example.com, we've sent a reset link",
    );
  });
});

describe('redirect after sign-in', () => {
  test.each([
    ['/entries/abc', '/entries/abc'],
    ['/settings', '/settings'],
    [undefined, '/'],
    ['', '/'],
    ['https://evil.example', '/'],
    ['//evil.example', '/'],
    ['/\\evil.example', '/'],
    ['/sign-in', '/'],
    ['/sign-up?x=1', '/'],
  ])('%j goes to %j', (param, target) => {
    expect(redirectTarget(param)).toBe(target);
  });

  test('sign-in remembers the requested route, except Pantry', () => {
    expect(signInHref('/entries/abc')).toEqual({ pathname: '/sign-in', params: { redirect: '/entries/abc' } });
    expect(signInHref('/')).toBe('/sign-in');
    expect(signInHref(null)).toBe('/sign-in');
  });
});

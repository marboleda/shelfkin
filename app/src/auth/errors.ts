// Readable messages for Firebase Auth errors. Screens show these, never a raw
// Firebase code such as `auth/invalid-credential`.

export const AUTH_MESSAGES = {
  wrongCredentials: 'Email or password is incorrect',
  emailInUse: 'An account with this email already exists',
  tooManyRequests: 'Too many attempts. Wait a few minutes and try again.',
  network: "Can't connect. Check your connection and try again.",
  invalidEmail: 'Enter a valid email address, like you@example.com',
  weakPassword: 'Choose a stronger password',
  generic: 'Something went wrong. Please try again.',
  sessionEnded: 'Your session has ended. Please sign in again.',
} as const;

/** Shown after "Forgot password?", the same whether or not the account exists. */
export function resetSentMessage(email: string): string {
  return `If an account exists for ${email}, we've sent a reset link`;
}

/** The Firebase error code (`auth/...`), or undefined for anything else. */
export function firebaseErrorCode(error: unknown): string | undefined {
  if (typeof error !== 'object' || error === null) return undefined;
  const code = (error as { code?: unknown }).code;
  return typeof code === 'string' ? code : undefined;
}

/**
 * The message for a failed Firebase Auth call. A wrong password, an unknown
 * email and a disabled account (`auth/user-disabled`, which Firebase only
 * returns when the password is correct) all give the same message, so the
 * screen never reveals whether an account exists or has been disabled.
 * Disabled-account attempts are recorded server-side in the Identity Platform
 * request logs (see "Auth request logs" in the README), not by the app.
 */
export function authErrorMessage(error: unknown): string {
  switch (firebaseErrorCode(error)) {
    case 'auth/invalid-credential':
    case 'auth/invalid-login-credentials':
    case 'auth/wrong-password':
    case 'auth/user-not-found':
    case 'auth/user-disabled':
      return AUTH_MESSAGES.wrongCredentials;
    case 'auth/email-already-in-use':
      return AUTH_MESSAGES.emailInUse;
    case 'auth/too-many-requests':
      return AUTH_MESSAGES.tooManyRequests;
    case 'auth/network-request-failed':
    case 'auth/timeout':
      return AUTH_MESSAGES.network;
    case 'auth/invalid-email':
    case 'auth/missing-email':
      return AUTH_MESSAGES.invalidEmail;
    case 'auth/weak-password':
      return AUTH_MESSAGES.weakPassword;
    default:
      return AUTH_MESSAGES.generic;
  }
}

/** Display name limits, the same rule `PATCH /me` applies (trimmed, 1 to 50 characters). */
export const NAME_MAX_LENGTH = 50;
export const PASSWORD_MIN_LENGTH = 8;

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/** Returns an error message for the name, or null when it is valid. Checks the trimmed value. */
export function validateName(name: string): string | null {
  const trimmed = name.trim();
  if (trimmed.length === 0) return 'Enter your name';
  // Count characters (code points), as the API does, not UTF-16 units.
  if ([...trimmed].length > NAME_MAX_LENGTH) return `Use ${NAME_MAX_LENGTH} characters or fewer`;
  return null;
}

export function validateEmail(email: string): string | null {
  const trimmed = email.trim();
  if (trimmed.length === 0) return 'Enter your email address';
  if (!EMAIL_PATTERN.test(trimmed)) return 'Enter a valid email address, like you@example.com';
  return null;
}

/** For new passwords on sign-up. Sign-in only checks that a password was typed. */
export function validateNewPassword(password: string): string | null {
  if (password.length === 0) return 'Enter a password';
  if (password.length < PASSWORD_MIN_LENGTH) return `Use at least ${PASSWORD_MIN_LENGTH} characters`;
  return null;
}

// Deleting the signed-in user's account (`DELETE /me`, #46).

import { useMutation } from '@tanstack/react-query';

import { apiClient, callApi } from '@/api/client';
import { isApiError, userMessage } from '@/api/errors';
import type { components } from '@/api/schema';

import { signOut } from './session';

export type HouseholdRef = components['schemas']['HouseholdRef'];

/** What the confirmation says will be deleted. */
export const DELETE_ACCOUNT_MESSAGE =
  'This permanently deletes your Shelfkin account and your sign-in. ' +
  "You'll leave every household you're in, and reminders stop on all your devices. " +
  "Households where you're the only member are deleted with all their pantry entries. " +
  'In shared households, the items you added stay, without your name. ' +
  "This can't be undone.";

/** The households to hand over first when `error` is a `must_transfer_ownership` 409, else null. */
export function householdsToTransfer(error: unknown): HouseholdRef[] | null {
  if (!isApiError(error) || error.status !== 409 || error.code !== 'must_transfer_ownership') return null;
  const households = (error.body as { error?: { households?: unknown } } | undefined)?.error?.households;
  if (!Array.isArray(households)) return [];
  return households.filter(
    (h): h is HouseholdRef =>
      typeof h === 'object' && h !== null && typeof h.id === 'string' && typeof h.name === 'string',
  );
}

/** A readable sentence naming the households to hand over, e.g. `"Home" and "Cabin"`. */
export function transferOwnershipMessage(households: HouseholdRef[]): string {
  if (households.length === 0) {
    return 'You own a household that has other members. Transfer its ownership to another member before deleting your account.';
  }
  const names = households.map((h) => `"${h.name}"`);
  const list = names.length === 1 ? names[0] : `${names.slice(0, -1).join(', ')} and ${names[names.length - 1]}`;
  return households.length === 1
    ? `You own ${list}, which has other members. Make another member its owner before deleting your account.`
    : `You own ${list}, which have other members. Make another member the owner of each before deleting your account.`;
}

/** A message for a failed deletion that is not a `must_transfer_ownership` 409. */
export function deleteAccountErrorMessage(error: unknown): string {
  if (isApiError(error) && error.code === 'account_deletion_failed') {
    return "Your account couldn't be deleted right now. Nothing was deleted; please try again later.";
  }
  return userMessage(error);
}

/**
 * `DELETE /me`, then sign out. The account is gone once the request succeeds,
 * so the sign-out skips the API calls a normal sign-out makes, and /sign-in
 * says the account was deleted.
 */
export async function deleteAccount(): Promise<void> {
  await callApi(() => apiClient.DELETE('/me'));
  try {
    await signOut({ accountDeleted: true });
  } catch (error) {
    // The account is already deleted; Firebase drops the session once its token
    // can no longer be refreshed. Nothing for the user to retry.
    console.warn('Sign-out after deleting the account failed', error);
  }
}

export function useDeleteAccountMutation() {
  return useMutation({ mutationFn: deleteAccount });
}

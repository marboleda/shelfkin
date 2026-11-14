// The signed-in user's households: `GET /households`, `POST /households` and
// `POST /invites/join`, plus the rules the create and join forms share.

import { apiClient, callApi } from '@/api/client';
import { isApiError, userMessage } from '@/api/errors';
import type { components } from '@/api/schema';

export type Household = components['schemas']['Household'];
export type HouseholdRole = components['schemas']['MemberRole'];
export type JoinInviteResult = components['schemas']['JoinInviteResult'];

/** Household name limit, the same rule `POST /households` applies (trimmed, 1 to 50 characters). */
export const HOUSEHOLD_NAME_MAX_LENGTH = 50;

export function fetchHouseholds(signal?: AbortSignal): Promise<Household[]> {
  return callApi(() => apiClient.GET('/households', { signal })).then((list) => sortHouseholds(list.households));
}

/** `POST /households`. Not idempotent, so it is never retried automatically. */
export function createHousehold(name: string): Promise<Household> {
  return callApi(() => apiClient.POST('/households', { body: { name: name.trim() } }));
}

/** `POST /invites/join`. The code is sent as typed: the API ignores case, spaces and dashes. */
export function joinHousehold(code: string): Promise<JoinInviteResult> {
  return callApi(() => apiClient.POST('/invites/join', { body: { code: code.trim() } }));
}

/** Sorted by name, ignoring case (as the API sorts them), then by ID so equal names keep a stable order. */
export function sortHouseholds(households: readonly Household[]): Household[] {
  return [...households].sort((a, b) => {
    const byName = a.name.localeCompare(b.name, undefined, { sensitivity: 'base' });
    if (byName !== 0) return byName;
    return a.id < b.id ? -1 : a.id > b.id ? 1 : 0;
  });
}

/** Adds `household` to the list, or replaces the one with the same ID, keeping the list sorted. */
export function withHousehold(households: readonly Household[] | undefined, household: Household): Household[] {
  return sortHouseholds([...(households ?? []).filter((item) => item.id !== household.id), household]);
}

type SegmenterLike = { segment(input: string): Iterable<{ segment: string }> };
type SegmenterConstructor = new (locale?: string, options?: { granularity: 'grapheme' }) => SegmenterLike;

/**
 * The first character of the trimmed name, uppercased, for the initial tile:
 * "home" gives "H". A name starting with an emoji gives that whole emoji
 * (including skin tones and flags where the platform can split graphemes).
 */
export function householdInitial(name: string): string {
  const trimmed = name.trim();
  if (!trimmed) return '';
  const Segmenter = (Intl as unknown as { Segmenter?: SegmenterConstructor }).Segmenter;
  let first: string | undefined;
  if (Segmenter) {
    for (const { segment } of new Segmenter(undefined, { granularity: 'grapheme' }).segment(trimmed)) {
      first = segment;
      break;
    }
  }
  first ??= Array.from(trimmed)[0];
  return (first ?? '').toUpperCase();
}

export function roleLabel(role: HouseholdRole): string {
  return role === 'owner' ? 'Owner' : 'Member';
}

/** Returns an error message for the household name, or null when it is valid. Checks the trimmed value. */
export function validateHouseholdName(name: string): string | null {
  const trimmed = name.trim();
  if (trimmed.length === 0) return 'Enter a name for your household';
  // Count characters (code points), as the API does, not UTF-16 units.
  if ([...trimmed].length > HOUSEHOLD_NAME_MAX_LENGTH) {
    return `Use ${HOUSEHOLD_NAME_MAX_LENGTH} characters or fewer`;
  }
  return null;
}

export function validateInviteCode(code: string): string | null {
  return code.replace(/[\s-]/g, '').length === 0 ? 'Enter the invite code' : null;
}

/** The message for a failed join, by the API's error `code`. */
export function joinErrorMessage(error: unknown): string {
  if (isApiError(error)) {
    switch (error.code) {
      case 'invite_not_found':
        return "That code doesn't match an invite. Check it and try again.";
      case 'invite_expired':
        return 'This invite has expired. Ask for a new one.';
      case 'invite_revoked':
        return 'This invite is no longer valid. Ask for a new one.';
    }
  }
  return userMessage(error);
}

export function joinedMessage(result: JoinInviteResult): string {
  return result.alreadyMember
    ? `You're already a member of ${result.household.name}`
    : `You joined ${result.household.name}`;
}

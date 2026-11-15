// The household settings screen's data: members (#15), invites (#16), and the
// owner's and members' actions on the current household (#14, #15, #16).
//
// Every query and mutation key starts with `['households', <id>]`, so a 404
// goes through the current-household provider's "no longer have access"
// handling. Owner-only requests can fail with 403 `owner_only` when ownership
// moved in another session; the screen handles that (see `isOwnerOnlyError`).

import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useRouter } from 'expo-router';

import { apiClient, callApi } from '@/api/client';
import { isApiError } from '@/api/errors';
import { queryKeys } from '@/api/query-keys';
import type { components } from '@/api/schema';

import { useCurrentHousehold, useHouseholdKeys, useHouseholds } from './current-household';
import { withHousehold, type Household } from './households';

export type Member = components['schemas']['Member'];
export type Invite = components['schemas']['Invite'];

/** Whether a request failed because the user is no longer the household's owner. */
export function isOwnerOnlyError(error: unknown): boolean {
  return isApiError(error) && error.status === 403 && error.code === 'owner_only';
}

// --- Formatting ------------------------------------------------------------------

/** An invite code as two groups of four: "ABCDEFGH" gives "ABCD-EFGH". */
export function formatInviteCode(code: string): string {
  const plain = code.replace(/[\s-]/g, '').toUpperCase();
  return plain.length > 4 ? `${plain.slice(0, 4)}-${plain.slice(4)}` : plain;
}

function startOfLocalDay(date: Date): number {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
}

/**
 * When an invite expires, in calendar days on this device: "Expires today",
 * "Expires tomorrow" or "Expires in 6 days".
 */
export function inviteExpiryLabel(expiresAt: string, now: Date = new Date()): string {
  const expires = new Date(expiresAt);
  // Rounded, so a daylight-saving change (a 23 or 25 hour day) still counts as one day.
  const days = Math.round((startOfLocalDay(expires) - startOfLocalDay(now)) / 86_400_000);
  if (days <= 0) return 'Expires today';
  if (days === 1) return 'Expires tomorrow';
  return `Expires in ${days} days`;
}

/** Whether an invite can still be used at `now`. One is never active at or after its `expiresAt`. */
export function isInviteActive(invite: Invite, now: number = Date.now()): boolean {
  return new Date(invite.expiresAt).getTime() > now;
}

/** The text the Share button sends. */
export function inviteShareMessage(householdName: string, invite: Invite): string {
  return `Join ${householdName} on Shelfkin: ${invite.url} (or enter code ${formatInviteCode(invite.code)})`;
}

/** Newest first, as the API sends them. */
function sortInvites(invites: readonly Invite[]): Invite[] {
  return [...invites].sort((a, b) => (a.createdAt < b.createdAt ? 1 : a.createdAt > b.createdAt ? -1 : 0));
}

// --- Requests --------------------------------------------------------------------

function fetchMembers(householdId: string, signal?: AbortSignal): Promise<Member[]> {
  return callApi(() =>
    apiClient.GET('/households/{householdId}/members', { params: { path: { householdId } }, signal }),
  ).then((list) => list.members);
}

function fetchInvites(householdId: string, signal?: AbortSignal): Promise<Invite[]> {
  return callApi(() =>
    apiClient.GET('/households/{householdId}/invites', { params: { path: { householdId } }, signal }),
  ).then((list) => sortInvites(list.invites));
}

// --- Queries ---------------------------------------------------------------------

/** `GET /households/{id}/members`: owner first, then by join date (the API's order). */
export function useMembersQuery() {
  const keys = useHouseholdKeys();
  return useQuery({
    queryKey: keys.members(),
    queryFn: ({ signal }) => fetchMembers(keys.householdId, signal),
  });
}

/**
 * `GET /households/{id}/invites`. Owner only: pass `enabled: false` for a
 * member, so the app never sends a request that would get a 403.
 */
export function useInvitesQuery(enabled: boolean) {
  const keys = useHouseholdKeys();
  return useQuery({
    queryKey: keys.invites(),
    queryFn: ({ signal }) => fetchInvites(keys.householdId, signal),
    enabled,
  });
}

// --- Mutations -------------------------------------------------------------------

/** `PATCH /households/{id}`. The new name shows in the household list (Pantry header, switcher). */
export function useRenameHouseholdMutation() {
  const keys = useHouseholdKeys();
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: keys.detail(),
    mutationFn: (name: string) =>
      callApi(() =>
        apiClient.PATCH('/households/{householdId}', {
          params: { path: { householdId: keys.householdId } },
          body: { name: name.trim() },
        }),
      ),
    onSuccess: (household: Household) => {
      queryClient.setQueryData<Household[]>(queryKeys.households.list(), (previous) =>
        previous ? withHousehold(previous, household) : previous,
      );
    },
  });
}

/** `POST /households/{id}/invites`. The new invite goes to the top of the list. */
export function useCreateInviteMutation() {
  const keys = useHouseholdKeys();
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: keys.invites(),
    mutationFn: () =>
      callApi(() =>
        apiClient.POST('/households/{householdId}/invites', { params: { path: { householdId: keys.householdId } } }),
      ),
    // Not idempotent: a retry after a lost response would create a second invite.
    retry: 0,
    onSuccess: (invite: Invite) => {
      queryClient.setQueryData<Invite[]>(keys.invites(), (previous) =>
        sortInvites([invite, ...(previous ?? []).filter((item) => item.id !== invite.id)]),
      );
    },
  });
}

/** `DELETE /households/{id}/invites/{inviteId}`. Succeeds for an invite that has just expired too. */
export function useRevokeInviteMutation() {
  const keys = useHouseholdKeys();
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: keys.invites(),
    mutationFn: (inviteId: string) =>
      callApi(() =>
        apiClient.DELETE('/households/{householdId}/invites/{inviteId}', {
          params: { path: { householdId: keys.householdId, inviteId } },
        }),
      ),
    onSuccess: (_data, inviteId) => {
      queryClient.setQueryData<Invite[]>(keys.invites(), (previous) => previous?.filter((item) => item.id !== inviteId));
    },
  });
}

function deleteMember(householdId: string, userId: string) {
  return callApi(() =>
    apiClient.DELETE('/households/{householdId}/members/{userId}', { params: { path: { householdId, userId } } }),
  );
}

/** `DELETE /households/{id}/members/{userId}` for another member (owner only). */
export function useRemoveMemberMutation() {
  const keys = useHouseholdKeys();
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: keys.members(),
    mutationFn: (userId: string) => deleteMember(keys.householdId, userId),
    onSuccess: (_data, userId) => {
      queryClient.setQueryData<Member[]>(keys.members(), (previous) => previous?.filter((item) => item.userId !== userId));
    },
  });
}

/**
 * `POST /households/{id}/transfer-ownership`. Afterwards the signed-in user is
 * a member: the household list says so straight away, so owner-only controls
 * disappear without a reload.
 */
export function useTransferOwnershipMutation() {
  const keys = useHouseholdKeys();
  const queryClient = useQueryClient();
  return useMutation({
    mutationKey: keys.members(),
    mutationFn: (userId: string) =>
      callApi(() =>
        apiClient.POST('/households/{householdId}/transfer-ownership', {
          params: { path: { householdId: keys.householdId } },
          body: { userId },
        }),
      ),
    onSuccess: (list) => {
      queryClient.setQueryData<Member[]>(keys.members(), list.members);
      queryClient.setQueryData<Household[]>(queryKeys.households.list(), (previous) =>
        previous?.map((item) => (item.id === keys.householdId ? { ...item, role: 'member' } : item)),
      );
      queryClient.removeQueries({ queryKey: keys.invites() });
    },
  });
}

/**
 * After leaving or deleting: go to Pantry, drop the household from the list
 * (so the app falls back to another household, or onboarding when none are
 * left, as in #26), and say what happened.
 */
function useAfterHouseholdGone() {
  const { forget, showNotice } = useHouseholds();
  const router = useRouter();
  return (householdId: string, message: string) => {
    router.navigate('/');
    forget(householdId);
    showNotice(message);
  };
}

/** Leaving: `DELETE /households/{id}/members/{me}`. Afterwards "You left <name>". */
export function useLeaveHouseholdMutation() {
  const keys = useHouseholdKeys();
  const { name } = useCurrentHousehold();
  const gone = useAfterHouseholdGone();
  return useMutation({
    mutationKey: keys.members(),
    mutationFn: (myUserId: string) => deleteMember(keys.householdId, myUserId),
    onSuccess: () => gone(keys.householdId, `You left ${name}`),
  });
}

/** `DELETE /households/{id}`: deletes it, with every entry, for every member. */
export function useDeleteHouseholdMutation() {
  const keys = useHouseholdKeys();
  const { name } = useCurrentHousehold();
  const gone = useAfterHouseholdGone();
  return useMutation({
    mutationKey: keys.detail(),
    mutationFn: () =>
      callApi(() =>
        apiClient.DELETE('/households/{householdId}', { params: { path: { householdId: keys.householdId } } }),
      ),
    onSuccess: () => gone(keys.householdId, `You deleted ${name}`),
  });
}

/**
 * After a 403 `owner_only`: reload the household list (so the role is fresh)
 * and the members, and forget the invites the user can no longer see.
 */
export function useReloadAfterOwnershipLost() {
  const keys = useHouseholdKeys();
  const queryClient = useQueryClient();
  return () => {
    queryClient.removeQueries({ queryKey: keys.invites() });
    return Promise.all([
      queryClient.invalidateQueries({ queryKey: queryKeys.households.list(), exact: true }),
      queryClient.invalidateQueries({ queryKey: keys.members(), exact: true }),
    ]);
  };
}

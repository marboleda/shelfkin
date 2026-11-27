// Changing and deleting one pantry entry, with optimistic updates.
//
// Each change is applied to the cached pantry at once (so Entry detail and
// Pantry both show it), sent as its own request with its own UUIDv7 `eventId`,
// and undone if the API rejects it. All the writes for one entry share a
// mutation `scope`, so TanStack Query sends them one at a time in the order
// they were made: an earlier change can never land after a later one.
//
// A failed change is undone by rebuilding the entry, not by putting back what
// was on screen before it: while an entry has writes pending, it keeps a
// confirmed version (what the last pantry fetch said, moved forward by every
// write that has succeeded since), and the screen shows that version with the
// changes still pending applied on top, in the order they were made. A failure
// takes its own change out of that list, so the entry goes straight to what
// the user should see: the latest change still on its way, or else the last
// value the server accepted, and never the value of another failed change.
//
// A failed request that may succeed later (a network error or a 5xx) is
// retried by TanStack Query with the same variables, so with the same
// `eventId`: the API applies it once, and a change that succeeds on a retry is
// never rolled back.
//
// Once the last pending write of the household has settled, the pantry is
// refetched so `lastChange` and `expiryStatus` come from the server. Refetching
// earlier would overwrite the optimistic values of the writes still pending.

import { useMutation, useQueryClient, type Mutation, type QueryClient } from '@tanstack/react-query';
import { useCallback, useEffect } from 'react';

import { sendWithKeepalive } from '@/api/client';
import { isApiError, userMessage } from '@/api/errors';
import { shouldRetryQuery } from '@/api/query-client';
import { queryKeys } from '@/api/query-keys';
import type { Me } from '@/auth/profile';
import { useHouseholds, useHouseholdKeys } from '@/household/current-household';
import { newId } from '@/lib/ids';
import { queryRetryDelay } from '@/lib/retry';

import { deleteEntry, updateEntry, type PantryEntry, type UpdateEntryBody } from './entries';
import {
  findEntry,
  provisionalExpiryStatus,
  removeEntryFrom,
  replaceEntryIn,
  restoreEntryIn,
  updateEntryIn,
  type RemovedEntry,
} from './entry-cache';
import type { LastChange, StockLevel } from './format';
import type { Pantry, PantryListEntry } from './pantry';

/** One field change. The API takes exactly one field per update. */
export type EntryChange =
  | { field: 'level'; value: StockLevel }
  | { field: 'locationId'; value: number }
  | { field: 'purchasedOn'; value: string }
  | { field: 'expiresOn'; value: string | null };

type EntryField = EntryChange['field'];

type UpdateVariables = {
  householdId: string;
  entryId: string;
  change: EntryChange;
  eventId: string;
  /** The item's name, for messages ("Couldn't update Milk. Try again."). */
  itemName: string;
  /**
   * The request already sent with `keepalive` as the page was hidden (see
   * `change`), taken by the first attempt; null once taken, or when the
   * request got no answer (a later attempt then sends it the usual way).
   */
  sent?: { response: Promise<PantryEntry | null> | null };
};

export type ChangeOptions = {
  /**
   * The page is being hidden, and may be closed or navigated away: send the
   * request at once with `keepalive` so it is not lost. Only done while no
   * other write to the entry is pending, so writes still land in order.
   */
  keepalive?: boolean;
};

type DeleteVariables = {
  householdId: string;
  entryId: string;
  eventId: string;
  itemName: string;
};

type DeleteContext = { removed: RemovedEntry | null };

/** The key of every write to one entry; household-scoped, so a 404 is traced to its household. */
export function entryMutationKey(householdId: string, entryId: string) {
  return [...queryKeys.households.pantry(householdId), entryId] as const;
}

/** The mutation scope that keeps one entry's writes in order. */
export function entryMutationScope(householdId: string, entryId: string) {
  return { id: `entry:${householdId}:${entryId}` };
}

const EVENT_TYPES: Record<EntryField, LastChange['type']> = {
  level: 'level_changed',
  locationId: 'location_changed',
  purchasedOn: 'purchase_date_changed',
  expiresOn: 'expiry_changed',
};

/** The `lastChange.changes` key of each field: the database column name. */
const CHANGE_KEYS: Record<EntryField, string> = {
  level: 'level',
  locationId: 'location_id',
  purchasedOn: 'purchased_on',
  expiresOn: 'expires_on',
};

function requestBody(change: EntryChange, eventId: string): UpdateEntryBody {
  switch (change.field) {
    case 'level':
      return { eventId, level: change.value };
    case 'locationId':
      return { eventId, locationId: change.value };
    case 'purchasedOn':
      return { eventId, purchasedOn: change.value };
    case 'expiresOn':
      return { eventId, expiresOn: change.value };
  }
}

/** The entry with `change` applied, and a "by you, just now" last change. */
function applyChange(entry: PantryListEntry, change: EntryChange, today: string, me: Me | undefined): PantryListEntry {
  const next = { ...entry, [change.field]: change.value } as PantryListEntry;
  next.expiryStatus = provisionalExpiryStatus(next, today);
  next.lastChange = {
    type: EVENT_TYPES[change.field],
    changes: { [CHANGE_KEYS[change.field]]: { from: entry[change.field], to: change.value } },
    actorId: me?.id ?? null,
    actorName: me?.displayName ?? null,
    occurredAt: new Date().toISOString(),
  };
  return next;
}

/** The message for a failed change: "Couldn't update Milk. Try again.", or why it failed when that helps. */
export function updateErrorMessage(itemName: string, error: unknown): string {
  if (isApiError(error) && (error.kind === 'network' || (error.status !== undefined && error.status < 500))) {
    return userMessage(error);
  }
  return `Couldn't update ${itemName}. Try again.`;
}

/** The message for a failed delete (other than a 404, which means it was already removed). */
export function deleteErrorMessage(itemName: string, error: unknown): string {
  if (isApiError(error) && (error.kind === 'network' || (error.status !== undefined && error.status < 500))) {
    return userMessage(error);
  }
  return `Couldn't delete ${itemName}. Try again.`;
}

function isNotFound(error: unknown): boolean {
  return isApiError(error) && error.status === 404;
}

/** Marks the mutations whose optimistic changes are applied on top of the confirmed entries. */
type EntryWriteMeta = { entryWrite: 'update' | 'delete' };

const UPDATE_META: EntryWriteMeta = { entryWrite: 'update' };
const DELETE_META: EntryWriteMeta = { entryWrite: 'delete' };

function isPantryKey(queryKey: readonly unknown[]): boolean {
  return queryKey.length === 3 && queryKey[0] === 'households' && queryKey[2] === 'entries';
}

/** A pending write to one entry. */
type PendingWrite =
  | { kind: 'update'; mutationId: number; variables: UpdateVariables }
  | { kind: 'delete'; mutationId: number; variables: DeleteVariables };

function asPendingWrite(mutation: Mutation<unknown, Error, unknown, unknown>): PendingWrite | null {
  const kind = (mutation.options.meta as EntryWriteMeta | undefined)?.entryWrite;
  const variables = mutation.state.variables;
  if (!kind || !variables) return null;
  return kind === 'update'
    ? { kind, mutationId: mutation.mutationId, variables: variables as UpdateVariables }
    : { kind, mutationId: mutation.mutationId, variables: variables as DeleteVariables };
}

/**
 * The pending writes of the household (of one entry, with `entryId`), in the
 * order they were made. Every write of a scope is pending from the moment it
 * is made, even while it waits for the ones before it, and it is still pending
 * while its own `onSuccess`, `onError` and `onSettled` run: those leave it out
 * with `exceptEventId`.
 */
function pendingWrites(
  queryClient: QueryClient,
  householdId: string,
  entryId?: string,
  exceptEventId?: string,
): PendingWrite[] {
  const mutationKey = entryId ? entryMutationKey(householdId, entryId) : queryKeys.households.pantry(householdId);
  return queryClient
    .getMutationCache()
    .findAll({ mutationKey, status: 'pending' })
    .map(asPendingWrite)
    .filter((write): write is PendingWrite => write !== null && write.variables.eventId !== exceptEventId)
    .sort((a, b) => a.mutationId - b.mutationId);
}

/**
 * The confirmed version of each entry that has writes pending, per
 * QueryClient, by `householdId:entryId`: what the server is known to have.
 *
 * - The first write of a run sets it to the entry on screen. No write to the
 *   entry is pending then, so nothing on screen is optimistic.
 * - A pantry fetch that answers while writes are pending replaces it with the
 *   fetched entry, before their changes are applied again on top of the answer.
 * - A write that succeeds applies its change to it.
 * - It is dropped once the entry's last pending write has settled.
 */
const confirmedEntries = new WeakMap<QueryClient, Map<string, PantryListEntry>>();

function confirmed(queryClient: QueryClient): Map<string, PantryListEntry> {
  let entries = confirmedEntries.get(queryClient);
  if (!entries) {
    entries = new Map();
    confirmedEntries.set(queryClient, entries);
  }
  return entries;
}

function confirmedKey(householdId: string, entryId: string): string {
  return `${householdId}:${entryId}`;
}

/**
 * Shows the entry as its confirmed version with the `pending` changes applied
 * on top, in order. Does nothing when there is no confirmed version or the
 * pantry no longer has the entry (a pending delete has taken it out).
 */
function showRebuiltEntry(queryClient: QueryClient, householdId: string, entryId: string, pending: PendingWrite[]) {
  const pantryKey = queryKeys.households.pantry(householdId);
  const pantry = queryClient.getQueryData<Pantry>(pantryKey);
  const base = confirmed(queryClient).get(confirmedKey(householdId, entryId));
  if (!pantry || !base) return;
  const me = queryClient.getQueryData<Me>(queryKeys.me());
  let entry = base;
  for (const write of pending) {
    if (write.kind === 'update') entry = applyChange(entry, write.variables.change, pantry.today, me);
  }
  queryClient.setQueryData<Pantry>(pantryKey, replaceEntryIn(pantry, entry));
}

/** Drops the entry's confirmed version, unless another write to it is still pending. */
function forgetWhenSettled(queryClient: QueryClient, variables: { householdId: string; entryId: string; eventId: string }) {
  if (pendingWrites(queryClient, variables.householdId, variables.entryId, variables.eventId).length === 0) {
    confirmed(queryClient).delete(confirmedKey(variables.householdId, variables.entryId));
  }
}

const guardedClients = new WeakSet<QueryClient>();

/**
 * Keeps the optimistic changes of pending writes on screen when a pantry
 * request that started after them (Pantry mounting, a pull to refresh)
 * answers with what the server had before they landed: the answer becomes the
 * confirmed version of each entry with writes pending, and their changes are
 * applied again on top of it, in the order they were made. Pantry requests
 * already on their way when a write starts are cancelled instead (see
 * `onMutate`). Installed once per QueryClient.
 */
function keepPendingWrites(queryClient: QueryClient) {
  if (guardedClients.has(queryClient)) return;
  guardedClients.add(queryClient);
  queryClient.getQueryCache().subscribe((event) => {
    if (event.type !== 'updated' || event.action.type !== 'success' || event.action.manual) return;
    const pantryKey = event.query.queryKey;
    if (!isPantryKey(pantryKey)) return;
    const householdId = String(pantryKey[1]);
    const pending = pendingWrites(queryClient, householdId);
    if (pending.length === 0) return;
    const fetched = event.query.state.data as Pantry | undefined;
    if (!fetched) return;
    const entries = confirmed(queryClient);
    for (const write of pending) {
      const found = findEntry(fetched, write.variables.entryId);
      const key = confirmedKey(householdId, write.variables.entryId);
      if (found) entries.set(key, found.entry);
      else entries.delete(key);
    }
    const me = queryClient.getQueryData<Me>(queryKeys.me());
    let pantry = fetched;
    for (const write of pending) {
      if (write.kind === 'update') {
        pantry = updateEntryIn(pantry, write.variables.entryId, (entry) =>
          applyChange(entry, write.variables.change, pantry.today, me),
        );
      } else {
        pantry = removeEntryFrom(pantry, write.variables.entryId)?.pantry ?? pantry;
      }
    }
    if (pantry !== fetched) queryClient.setQueryData<Pantry>(pantryKey, pantry);
  });
}

/** Refetches the household's pantry, unless another write to it is still pending (this one counts as pending). */
function refreshWhenSettled(queryClient: QueryClient, householdId: string) {
  const pantryKey = queryKeys.households.pantry(householdId);
  if (queryClient.isMutating({ mutationKey: pantryKey }) <= 1) {
    void queryClient.invalidateQueries({ queryKey: pantryKey });
  }
}

/**
 * Changes one field of one entry of the current household. `change(field,
 * itemName)` applies it optimistically and sends it with a new `eventId`; the
 * caller decides whether there is anything to change.
 */
export function useUpdateEntry(entryId: string) {
  const queryClient = useQueryClient();
  const { householdId } = useHouseholdKeys();
  const { showNotice } = useHouseholds();

  useEffect(() => keepPendingWrites(queryClient), [queryClient]);

  const mutation = useMutation<unknown, unknown, UpdateVariables>({
    mutationKey: entryMutationKey(householdId, entryId),
    meta: UPDATE_META,
    scope: entryMutationScope(householdId, entryId),
    mutationFn: async (variables) => {
      const sent = variables.sent?.response;
      if (sent && variables.sent) {
        variables.sent.response = null;
        const entry = await sent;
        if (entry) return entry;
      }
      return updateEntry(variables.householdId, variables.entryId, requestBody(variables.change, variables.eventId));
    },
    retry: shouldRetryQuery,
    retryDelay: queryRetryDelay(queryClient),
    onMutate: async (variables) => {
      const pantryKey = queryKeys.households.pantry(variables.householdId);
      // A pantry request already on its way would overwrite the optimistic value with the old one.
      await queryClient.cancelQueries({ queryKey: pantryKey });
      const found = findEntry(queryClient.getQueryData<Pantry>(pantryKey), variables.entryId);
      if (!found) return;
      const entries = confirmed(queryClient);
      const key = confirmedKey(variables.householdId, variables.entryId);
      // Only the first write of a run takes the entry on screen as confirmed. A pantry fetch that
      // answers while this write is starting (this write is pending by then) sets the confirmed
      // version to the fetched entry itself, so this change never becomes its own rollback value.
      if (!entries.has(key)) entries.set(key, found.entry);
      showRebuiltEntry(
        queryClient,
        variables.householdId,
        variables.entryId,
        pendingWrites(queryClient, variables.householdId, variables.entryId),
      );
    },
    onSuccess: (_data, variables) => {
      const entries = confirmed(queryClient);
      const key = confirmedKey(variables.householdId, variables.entryId);
      const base = entries.get(key);
      const pantry = queryClient.getQueryData<Pantry>(queryKeys.households.pantry(variables.householdId));
      if (!base || !pantry) return;
      const me = queryClient.getQueryData<Me>(queryKeys.me());
      entries.set(key, applyChange(base, variables.change, pantry.today, me));
    },
    onError: (error, variables) => {
      const pantryKey = queryKeys.households.pantry(variables.householdId);
      if (isNotFound(error)) {
        // Deleted by someone else: Entry detail now says it isn't in the household any more.
        const pantry = queryClient.getQueryData<Pantry>(pantryKey);
        const result = pantry ? removeEntryFrom(pantry, variables.entryId) : null;
        if (result) queryClient.setQueryData<Pantry>(pantryKey, result.pantry);
        return;
      }
      // The confirmed entry with the other pending changes on top: this change is gone, the others stay.
      const others = pendingWrites(queryClient, variables.householdId, variables.entryId, variables.eventId);
      showRebuiltEntry(queryClient, variables.householdId, variables.entryId, others);
      // A later change to the same field, still on its way, has replaced this one: only its own failure counts.
      const replaced = others.some(
        (write) => write.kind === 'update' && write.variables.change.field === variables.change.field,
      );
      if (!replaced) showNotice(updateErrorMessage(variables.itemName, error));
    },
    onSettled: (_data, _error, variables) => {
      forgetWhenSettled(queryClient, variables);
      refreshWhenSettled(queryClient, variables.householdId);
    },
  });

  const { mutate } = mutation;
  const change = useCallback(
    (next: EntryChange, itemName: string, options: ChangeOptions = {}) => {
      const eventId = newId();
      let sent: UpdateVariables['sent'];
      if (options.keepalive && queryClient.isMutating({ mutationKey: entryMutationKey(householdId, entryId) }) === 0) {
        // Sent before `mutate`, whose first steps are asynchronous and may not run before the page unloads.
        const path = `/households/${encodeURIComponent(householdId)}/entries/${encodeURIComponent(entryId)}`;
        const response = sendWithKeepalive<PantryEntry>('PATCH', path, requestBody(next, eventId));
        // The mutation reads the outcome once it starts; don't report a failure as unhandled before then.
        response.catch(() => {});
        sent = { response };
      }
      mutate({ householdId, entryId, change: next, eventId, itemName, sent });
    },
    [mutate, queryClient, householdId, entryId],
  );

  return { change };
}

/**
 * Deletes one entry of the current household. `remove(itemName)` takes it out
 * of the cached pantry at once and sends the delete with a new `eventId`; a
 * failure puts it back. A 404 means someone already removed it: it stays removed.
 */
export function useDeleteEntry(entryId: string) {
  const queryClient = useQueryClient();
  const { householdId } = useHouseholdKeys();
  const { showNotice } = useHouseholds();

  useEffect(() => keepPendingWrites(queryClient), [queryClient]);

  const mutation = useMutation<void, unknown, DeleteVariables, DeleteContext>({
    mutationKey: entryMutationKey(householdId, entryId),
    meta: DELETE_META,
    scope: entryMutationScope(householdId, entryId),
    mutationFn: (variables) => deleteEntry(variables.householdId, variables.entryId, variables.eventId),
    retry: shouldRetryQuery,
    retryDelay: queryRetryDelay(queryClient),
    onMutate: async (variables) => {
      const pantryKey = queryKeys.households.pantry(variables.householdId);
      await queryClient.cancelQueries({ queryKey: pantryKey });
      const pantry = queryClient.getQueryData<Pantry>(pantryKey);
      const result = pantry ? removeEntryFrom(pantry, variables.entryId) : null;
      if (!result) return { removed: null };
      queryClient.setQueryData<Pantry>(pantryKey, result.pantry);
      return { removed: result.removed };
    },
    onError: (error, variables, context) => {
      if (isNotFound(error)) {
        showNotice(`${variables.itemName} was already removed`);
        return;
      }
      const pantryKey = queryKeys.households.pantry(variables.householdId);
      const pantry = queryClient.getQueryData<Pantry>(pantryKey);
      if (pantry && context?.removed) {
        queryClient.setQueryData<Pantry>(pantryKey, restoreEntryIn(pantry, context.removed));
      }
      showNotice(deleteErrorMessage(variables.itemName, error));
    },
    onSettled: (_data, _error, variables) => {
      forgetWhenSettled(queryClient, variables);
      refreshWhenSettled(queryClient, variables.householdId);
    },
  });

  const { mutate } = mutation;
  const remove = useCallback(
    (itemName: string) => {
      mutate({ householdId, entryId, eventId: newId(), itemName });
    },
    [mutate, householdId, entryId],
  );

  return { remove };
}

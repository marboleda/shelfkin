// Changing and deleting one pantry entry, with optimistic updates.
//
// Each change is applied to the cached pantry at once (so Entry detail and
// Pantry both show it), sent as its own request with its own UUIDv7 `eventId`,
// and undone if the API rejects it. All the writes for one entry share a
// mutation `scope`, so TanStack Query sends them one at a time in the order
// they were made: an earlier change can never land after a later one.
//
// A failed request that may succeed later (a network error or a 5xx) is
// retried by TanStack Query with the same variables, so with the same
// `eventId`: the API applies it once, and a change that succeeds on a retry is
// never rolled back.
//
// Once the last pending write of the household has settled, the pantry is
// refetched so `lastChange` and `expiryStatus` come from the server. Refetching
// earlier would overwrite the optimistic values of the writes still pending.

import { useMutation, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useCallback, useEffect } from 'react';

import { isApiError, userMessage } from '@/api/errors';
import { shouldRetryQuery } from '@/api/query-client';
import { queryKeys } from '@/api/query-keys';
import type { Me } from '@/auth/profile';
import { useHouseholds, useHouseholdKeys } from '@/household/current-household';
import { newId } from '@/lib/ids';
import { queryRetryDelay } from '@/lib/retry';

import { deleteEntry, updateEntry, type UpdateEntryBody } from './entries';
import { findEntry, provisionalExpiryStatus, removeEntryFrom, restoreEntryIn, updateEntryIn, type RemovedEntry } from './entry-cache';
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
};

/** What the entry was before the change, to put back if it fails. */
type UpdateContext = {
  previous: Pick<PantryListEntry, 'expiryStatus' | 'lastChange'> & { change: EntryChange };
} | null;

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

function currentValue(entry: PantryListEntry, field: EntryField): EntryChange {
  return { field, value: entry[field] } as EntryChange;
}

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

/** Marks the mutations whose optimistic changes `keepPendingWrites` re-applies. */
type EntryWriteMeta = { entryWrite: 'update' | 'delete' };

const UPDATE_META: EntryWriteMeta = { entryWrite: 'update' };
const DELETE_META: EntryWriteMeta = { entryWrite: 'delete' };

function isPantryKey(queryKey: readonly unknown[]): boolean {
  return queryKey.length === 3 && queryKey[0] === 'households' && queryKey[2] === 'entries';
}

const guardedClients = new WeakSet<QueryClient>();

/**
 * Keeps the optimistic changes of pending writes on screen when a pantry
 * request that started after them (Pantry mounting, a pull to refresh)
 * answers with what the server had before they landed: their changes are
 * applied again on top of the answer, in the order they were made. Pantry
 * requests already on their way when a write starts are cancelled instead
 * (see `onMutate`). Installed once per QueryClient.
 */
function keepPendingWrites(queryClient: QueryClient) {
  if (guardedClients.has(queryClient)) return;
  guardedClients.add(queryClient);
  queryClient.getQueryCache().subscribe((event) => {
    if (event.type !== 'updated' || event.action.type !== 'success' || event.action.manual) return;
    const pantryKey = event.query.queryKey;
    if (!isPantryKey(pantryKey)) return;
    const pending = queryClient
      .getMutationCache()
      .findAll({ mutationKey: pantryKey, status: 'pending' })
      .filter((mutation) => (mutation.options.meta as EntryWriteMeta | undefined)?.entryWrite);
    if (pending.length === 0) return;
    const fetched = event.query.state.data as Pantry | undefined;
    if (!fetched) return;
    const me = queryClient.getQueryData<Me>(queryKeys.me());
    let pantry = fetched;
    for (const mutation of pending) {
      const kind = (mutation.options.meta as EntryWriteMeta).entryWrite;
      if (kind === 'update') {
        const variables = mutation.state.variables as UpdateVariables | undefined;
        if (!variables) continue;
        pantry = updateEntryIn(pantry, variables.entryId, (entry) => applyChange(entry, variables.change, pantry.today, me));
      } else {
        const variables = mutation.state.variables as DeleteVariables | undefined;
        if (!variables) continue;
        pantry = removeEntryFrom(pantry, variables.entryId)?.pantry ?? pantry;
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

  const mutation = useMutation<unknown, unknown, UpdateVariables, UpdateContext>({
    mutationKey: entryMutationKey(householdId, entryId),
    meta: UPDATE_META,
    scope: entryMutationScope(householdId, entryId),
    mutationFn: (variables) =>
      updateEntry(variables.householdId, variables.entryId, requestBody(variables.change, variables.eventId)),
    retry: shouldRetryQuery,
    retryDelay: queryRetryDelay(queryClient),
    onMutate: async (variables) => {
      const pantryKey = queryKeys.households.pantry(variables.householdId);
      // A pantry request already on its way would overwrite the optimistic value with the old one.
      await queryClient.cancelQueries({ queryKey: pantryKey });
      const pantry = queryClient.getQueryData<Pantry>(pantryKey);
      const found = findEntry(pantry, variables.entryId);
      if (!pantry || !found) return null;
      const me = queryClient.getQueryData<Me>(queryKeys.me());
      queryClient.setQueryData<Pantry>(
        pantryKey,
        updateEntryIn(pantry, variables.entryId, (entry) => applyChange(entry, variables.change, pantry.today, me)),
      );
      return {
        previous: {
          change: currentValue(found.entry, variables.change.field),
          expiryStatus: found.entry.expiryStatus,
          lastChange: found.entry.lastChange,
        },
      };
    },
    onError: (error, variables, context) => {
      const pantryKey = queryKeys.households.pantry(variables.householdId);
      const pantry = queryClient.getQueryData<Pantry>(pantryKey);
      if (!pantry) return;
      if (isNotFound(error)) {
        // Deleted by someone else: Entry detail now says it isn't in the household any more.
        const result = removeEntryFrom(pantry, variables.entryId);
        if (result) queryClient.setQueryData<Pantry>(pantryKey, result.pantry);
        return;
      }
      const previous = context?.previous;
      const found = findEntry(pantry, variables.entryId);
      // Put the old value back, unless a later change to the same field has replaced this one.
      if (previous && found && found.entry[variables.change.field] === variables.change.value) {
        queryClient.setQueryData<Pantry>(
          pantryKey,
          updateEntryIn(
            pantry,
            variables.entryId,
            (entry) =>
              ({
                ...entry,
                [previous.change.field]: previous.change.value,
                expiryStatus: previous.expiryStatus,
                lastChange: previous.lastChange,
              }) as PantryListEntry,
          ),
        );
      }
      showNotice(updateErrorMessage(variables.itemName, error));
    },
    onSettled: (_data, _error, variables) => refreshWhenSettled(queryClient, variables.householdId),
  });

  const { mutate } = mutation;
  const change = useCallback(
    (next: EntryChange, itemName: string) => {
      mutate({ householdId, entryId, change: next, eventId: newId(), itemName });
    },
    [mutate, householdId, entryId],
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
    onSettled: (_data, _error, variables) => refreshWhenSettled(queryClient, variables.householdId),
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

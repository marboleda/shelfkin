// One catalog item as the current household sees it
// (`GET /households/{householdId}/catalog-items/{itemId}`), and changing the
// household's defaults for it (`PATCH` on the same path).
//
// Each field (category, location, shelf life) is saved on its own request.
// A chosen value is shown at once and put back if the request fails; a reset
// (`null`) waits for the API, which alone knows the built-in default. All the
// writes for one item share a mutation `scope`, so they reach the API in the
// order they were made.
//
// After a change the household's pantry (entries take their item's category)
// and catalog search results (Add item prefills the default location) are
// refetched. Defaults record no item event, so no entry's last change moves.

import { useMutation, useMutationState, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useCallback } from 'react';

import { apiClient, callApi } from '@/api/client';
import { isApiError, userMessage } from '@/api/errors';
import { shouldRetryQuery } from '@/api/query-client';
import { queryKeys } from '@/api/query-keys';
import type { components } from '@/api/schema';
import { useHouseholds, useHouseholdKeys } from '@/household/current-household';
import { queryRetryDelay } from '@/lib/retry';

import { catalogQueriesKey, MAX_SHELF_LIFE_DAYS, MIN_SHELF_LIFE_DAYS, type CatalogItem } from './catalog';

export type UpdateCatalogItemBody = components['schemas']['UpdateCatalogItem'];

/** One default to change. `null` resets a built-in item's category or location, or clears a shelf life. */
export type DefaultsChange =
  | { field: 'categoryId'; value: number | null }
  | { field: 'locationId'; value: number | null }
  | { field: 'shelfLifeDays'; value: number | null };

export type DefaultsField = DefaultsChange['field'];

type UpdateVariables = {
  householdId: string;
  itemId: string;
  change: DefaultsChange;
  /** The item's name, for messages ("Couldn't update Milk. Try again."). */
  itemName: string;
};

/** The field as it was before the change, to put back if the request fails. */
type FieldState = Pick<CatalogItem, 'categoryId' | 'locationId' | 'shelfLifeDays' | 'categoryOverridden' | 'locationOverridden'>;
type UpdateContext = { previous: Partial<FieldState>; optimistic: Partial<FieldState> } | null;

export function fetchCatalogItem(householdId: string, itemId: string, signal?: AbortSignal): Promise<CatalogItem> {
  return callApi(() =>
    apiClient.GET('/households/{householdId}/catalog-items/{itemId}', {
      params: { path: { householdId, itemId } },
      signal,
    }),
  );
}

export function updateCatalogItem(
  householdId: string,
  itemId: string,
  body: UpdateCatalogItemBody,
  signal?: AbortSignal,
): Promise<CatalogItem> {
  return callApi(() =>
    apiClient.PATCH('/households/{householdId}/catalog-items/{itemId}', {
      params: { path: { householdId, itemId } },
      body,
      signal,
    }),
  );
}

/** A whole number of days from 1 to 3650, or null for anything else. */
export function parseShelfLifeDays(text: string): number | null {
  const trimmed = text.trim();
  if (!/^\d+$/.test(trimmed)) return null;
  const days = Number(trimmed);
  return days >= MIN_SHELF_LIFE_DAYS && days <= MAX_SHELF_LIFE_DAYS ? days : null;
}

/** The key of every write to one item's defaults; household-scoped, so a 404 is traced to its household. */
export function catalogItemMutationKey(householdId: string, itemId: string) {
  return queryKeys.households.catalogItem(householdId, itemId);
}

/** The current household's view of one catalog item. A 404 (unknown, or another household's custom item) is not retried. */
export function useCatalogItemQuery(itemId: string) {
  const { householdId } = useHouseholdKeys();
  return useQuery({
    queryKey: queryKeys.households.catalogItem(householdId, itemId),
    queryFn: ({ signal }) => fetchCatalogItem(householdId, itemId, signal),
    enabled: itemId !== '',
  });
}

/** The fields of the item that a change to `field` touches. */
function fieldsOf(field: DefaultsField): (keyof FieldState)[] {
  switch (field) {
    case 'categoryId':
      return ['categoryId', 'categoryOverridden'];
    case 'locationId':
      return ['locationId', 'locationOverridden'];
    case 'shelfLifeDays':
      return ['shelfLifeDays'];
  }
}

function pick(item: CatalogItem, fields: (keyof FieldState)[]): Partial<FieldState> {
  const picked: Partial<FieldState> = {};
  for (const field of fields) (picked as Record<string, unknown>)[field] = item[field];
  return picked;
}

function sameFields(item: CatalogItem, fields: Partial<FieldState>): boolean {
  return Object.entries(fields).every(([field, value]) => item[field as keyof FieldState] === value);
}

/** What the item shows while a change is on its way. A reset shows nothing new until the API answers. */
function optimisticFields(item: CatalogItem, change: DefaultsChange): Partial<FieldState> {
  if (change.value === null && change.field !== 'shelfLifeDays') return {};
  switch (change.field) {
    case 'categoryId':
      return { categoryId: change.value as number, categoryOverridden: item.isCustom ? false : true };
    case 'locationId':
      return { locationId: change.value as number, locationOverridden: item.isCustom ? false : true };
    case 'shelfLifeDays':
      return { shelfLifeDays: change.value };
  }
}

function requestBody(change: DefaultsChange): UpdateCatalogItemBody {
  switch (change.field) {
    case 'categoryId':
      return { categoryId: change.value };
    case 'locationId':
      return { locationId: change.value };
    case 'shelfLifeDays':
      return { shelfLifeDays: change.value };
  }
}

/** The message for a failed change: "Couldn't update Milk. Try again.", or why it failed when that helps. */
export function defaultsErrorMessage(itemName: string, error: unknown): string {
  if (isApiError(error) && (error.kind === 'network' || (error.status !== undefined && error.status < 500))) {
    return userMessage(error);
  }
  return `Couldn't update ${itemName}. Try again.`;
}

/** Refetches what shows the item's defaults: the household's pantry (categories) and its catalog search results (locations). */
export function refreshAfterDefaultsChange(queryClient: QueryClient, householdId: string) {
  void queryClient.invalidateQueries({ queryKey: queryKeys.households.pantry(householdId) });
  void queryClient.invalidateQueries({ queryKey: catalogQueriesKey(householdId) });
}

/**
 * Changes the current household's defaults for one catalog item.
 * `change(next, itemName)` sends one field; the caller decides whether there
 * is anything to change. `pending` lists the fields being saved.
 */
export function useUpdateCatalogItem(itemId: string) {
  const queryClient = useQueryClient();
  const { householdId } = useHouseholdKeys();
  const { showNotice } = useHouseholds();
  const mutationKey = catalogItemMutationKey(householdId, itemId);

  const mutation = useMutation<CatalogItem, unknown, UpdateVariables, UpdateContext>({
    mutationKey,
    scope: { id: `catalog-item:${householdId}:${itemId}` },
    mutationFn: (variables) => updateCatalogItem(variables.householdId, variables.itemId, requestBody(variables.change)),
    retry: shouldRetryQuery,
    retryDelay: queryRetryDelay(queryClient),
    onMutate: async (variables) => {
      const itemKey = queryKeys.households.catalogItem(variables.householdId, variables.itemId);
      // A request already on its way would overwrite the new value with the old one.
      await queryClient.cancelQueries({ queryKey: itemKey });
      const item = queryClient.getQueryData<CatalogItem>(itemKey);
      if (!item) return null;
      const optimistic = optimisticFields(item, variables.change);
      const previous = pick(item, Object.keys(optimistic) as (keyof FieldState)[]);
      if (Object.keys(optimistic).length > 0) queryClient.setQueryData<CatalogItem>(itemKey, { ...item, ...optimistic });
      return { previous, optimistic };
    },
    onSuccess: (saved, variables) => {
      const itemKey = queryKeys.households.catalogItem(variables.householdId, variables.itemId);
      const item = queryClient.getQueryData<CatalogItem>(itemKey);
      // Only this change's fields: the answer has not seen a later change to another field yet.
      const fields = pick(saved, fieldsOf(variables.change.field));
      queryClient.setQueryData<CatalogItem>(itemKey, item ? { ...item, ...fields } : saved);
    },
    onError: (error, variables, context) => {
      const itemKey = queryKeys.households.catalogItem(variables.householdId, variables.itemId);
      const item = queryClient.getQueryData<CatalogItem>(itemKey);
      // Put the old value back, unless a later change to the same field has replaced this one.
      if (item && context && Object.keys(context.previous).length > 0 && sameFields(item, context.optimistic)) {
        queryClient.setQueryData<CatalogItem>(itemKey, { ...item, ...context.previous });
      }
      showNotice(defaultsErrorMessage(variables.itemName, error));
    },
    onSettled: (_data, error, variables) => {
      if (!error) refreshAfterDefaultsChange(queryClient, variables.householdId);
    },
  });

  const pending = useMutationState({
    filters: { mutationKey, status: 'pending', exact: true },
    select: (m) => (m.state.variables as UpdateVariables | undefined)?.change.field,
  });

  const { mutate } = mutation;
  const change = useCallback(
    (next: DefaultsChange, itemName: string) => {
      mutate({ householdId, itemId, change: next, itemName });
    },
    [mutate, householdId, itemId],
  );

  return { change, pending: pending.filter((field): field is DefaultsField => field !== undefined) };
}

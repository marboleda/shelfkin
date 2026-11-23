// The household's item catalog: type-ahead search
// (`GET /households/{householdId}/catalog/search`) and creating a custom item
// (`POST /households/{householdId}/catalog-items`).

import { useQuery } from '@tanstack/react-query';

import { apiClient, callApi } from '@/api/client';
import type { components } from '@/api/schema';
import { useHouseholdKeys } from '@/household/current-household';

export type CatalogItem = components['schemas']['CatalogItem'];
export type CreateCatalogItemBody = components['schemas']['CreateCatalogItem'];

/** The longest `q` the search accepts (after trimming). */
export const MAX_SEARCH_LENGTH = 100;
/** The longest item name the API accepts (after cleaning). */
export const MAX_ITEM_NAME_LENGTH = 60;
/** The shelf life a custom item can have, in days. */
export const MIN_SHELF_LIFE_DAYS = 1;
export const MAX_SHELF_LIFE_DAYS = 3650;

/** The cleaning the API does to a new item's name: trimmed, inner runs of whitespace collapsed to one space. */
export function cleanItemName(name: string): string {
  return name.trim().replace(/\s+/g, ' ');
}

/** Whether two names are the same item name, ignoring case and extra spaces. */
export function sameItemName(a: string, b: string): boolean {
  return cleanItemName(a).toLowerCase() === cleanItemName(b).toLowerCase();
}

export function searchCatalog(householdId: string, query: string, signal?: AbortSignal): Promise<CatalogItem[]> {
  return callApi(() =>
    apiClient.GET('/households/{householdId}/catalog/search', {
      params: { path: { householdId }, query: { q: query } },
      signal,
    }),
  ).then((list) => list.items);
}

export function createCatalogItem(
  householdId: string,
  body: CreateCatalogItemBody,
  signal?: AbortSignal,
): Promise<CatalogItem> {
  return callApi(() =>
    apiClient.POST('/households/{householdId}/catalog-items', { params: { path: { householdId } }, body, signal }),
  );
}

/**
 * Catalog search results for `query` (already trimmed), cached under
 * `keys.catalogSearch(query)`. An empty query sends no request.
 *
 * While a new query loads, the previous query's results stay as placeholder
 * data (`isPlaceholderData`), but only within the same household. Results are
 * always those of the query asked for last: a slow response for older text
 * lands in its own cache entry and is never shown in place of newer results.
 */
export function useCatalogSearchQuery(query: string) {
  const keys = useHouseholdKeys();
  const householdId = keys.householdId;
  return useQuery({
    queryKey: keys.catalogSearch(query),
    queryFn: ({ signal }) => searchCatalog(householdId, query, signal),
    enabled: query !== '',
    placeholderData: (previous, previousQuery) =>
      previousQuery && previousQuery.queryKey[1] === householdId ? previous : undefined,
  });
}

/** Every catalog query of a household (search results), to invalidate after the catalog changes. */
export function catalogQueriesKey(householdId: string) {
  return ['households', householdId, 'catalog'] as const;
}

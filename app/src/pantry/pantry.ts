// The current household's pantry (`GET /households/{householdId}/pantry`) and
// the storage locations (`GET /locations`) whose names it shows.

import { useQuery } from '@tanstack/react-query';
import { useMemo } from 'react';

import { apiClient, callApi } from '@/api/client';
import { queryKeys } from '@/api/query-keys';
import type { components } from '@/api/schema';
import { useHouseholdKeys } from '@/household/current-household';

export type Pantry = components['schemas']['Pantry'];
export type PantryGroup = components['schemas']['PantryGroup'];
export type PantryListEntry = components['schemas']['PantryListEntry'];
export type Location = components['schemas']['Location'];

export function fetchPantry(householdId: string, signal?: AbortSignal): Promise<Pantry> {
  return callApi(() =>
    apiClient.GET('/households/{householdId}/pantry', { params: { path: { householdId } }, signal }),
  );
}

export function fetchLocations(signal?: AbortSignal): Promise<Location[]> {
  return callApi(() => apiClient.GET('/locations', { signal })).then((list) => list.items);
}

/**
 * The current household's whole pantry, cached under
 * `queryKeys.households.pantry(householdId)`. Anything that changes an entry
 * (Add item, Entry detail) invalidates that key to refresh this list.
 */
export function usePantryQuery() {
  const keys = useHouseholdKeys();
  return useQuery({
    queryKey: keys.pantry(),
    queryFn: ({ signal }) => fetchPantry(keys.householdId, signal),
  });
}

/** The built-in locations. They never change while the app runs, so they are fetched once. */
export function useLocationsQuery() {
  return useQuery({
    queryKey: queryKeys.locations(),
    queryFn: ({ signal }) => fetchLocations(signal),
    staleTime: Infinity,
  });
}

/** A lookup from location ID to name; unknown (or still loading) IDs give undefined. */
export function useLocationNames(): (id: number) => string | undefined {
  const { data } = useLocationsQuery();
  return useMemo(() => {
    const names = new Map((data ?? []).map((location) => [location.id, location.name]));
    return (id: number) => names.get(id);
  }, [data]);
}

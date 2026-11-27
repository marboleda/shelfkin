// Writes to pantry entries (`/households/{householdId}/entries`). Every write
// carries client-generated UUIDv7s from src/lib/ids.ts, so repeating a request
// is applied once.

import { apiClient, callApi } from '@/api/client';
import type { components } from '@/api/schema';

export type PantryEntry = components['schemas']['PantryEntry'];
export type CreateEntryBody = components['schemas']['CreateEntry'];

/** Error codes of a 409 that mean the IDs were already used for something else: make new ones. */
export const ID_CONFLICT_CODES: readonly string[] = ['entry_id_conflict', 'event_id_conflict'];

/**
 * `POST /households/{householdId}/entries`. A 201 (created) and a 200 (a retry
 * the API had already applied) both resolve with the entry.
 */
export function createEntry(householdId: string, body: CreateEntryBody, signal?: AbortSignal): Promise<PantryEntry> {
  return callApi(() =>
    apiClient.POST('/households/{householdId}/entries', { params: { path: { householdId } }, body, signal }),
  );
}

export type UpdateEntryBody = components['schemas']['UpdateEntry'];

/**
 * `PATCH /households/{householdId}/entries/{entryId}`: changes exactly one
 * field. Repeating it with the same `eventId` is applied once.
 */
export function updateEntry(
  householdId: string,
  entryId: string,
  body: UpdateEntryBody,
  signal?: AbortSignal,
): Promise<PantryEntry> {
  return callApi(() =>
    apiClient.PATCH('/households/{householdId}/entries/{entryId}', {
      params: { path: { householdId, entryId } },
      body,
      signal,
    }),
  );
}

/**
 * `DELETE /households/{householdId}/entries/{entryId}?eventId=...`. Repeating
 * it with the same `eventId` after it succeeded answers 204 again.
 */
export async function deleteEntry(
  householdId: string,
  entryId: string,
  eventId: string,
  signal?: AbortSignal,
): Promise<void> {
  await callApi(() =>
    apiClient.DELETE('/households/{householdId}/entries/{entryId}', {
      params: { path: { householdId, entryId }, query: { eventId } },
      signal,
    }),
  );
}

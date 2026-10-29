/**
 * Every TanStack Query key in the app comes from here, so a feature that
 * invalidates or updates a query uses exactly the key the query was cached
 * under. Add a group per resource, from general to specific, e.g.
 *
 *   households: {
 *     all: () => ['households'] as const,
 *     detail: (id: string) => ['households', id] as const,
 *   },
 */
export const queryKeys = {
  health: () => ['health'] as const,
};

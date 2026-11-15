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
  /** The signed-in user's profile (`GET /me`). */
  me: () => ['me'] as const,
  /**
   * Households, and everything scoped to one. Every household-scoped key
   * starts with `['households', householdId]`, so switching household never
   * reads another household's cache, and a 404 can be traced to its household
   * (see `householdIdOfKey`). Read the current household's keys through
   * `useHouseholdKeys()` in src/household/current-household.tsx.
   */
  households: {
    all: () => ['households'] as const,
    /** `GET /households`. `'list'` can never clash with a household ID (a UUID). */
    list: () => ['households', 'list'] as const,
    detail: (householdId: string) => ['households', householdId] as const,
    pantry: (householdId: string) => ['households', householdId, 'entries'] as const,
    members: (householdId: string) => ['households', householdId, 'members'] as const,
    invites: (householdId: string) => ['households', householdId, 'invites'] as const,
    catalogSearch: (householdId: string, query: string) =>
      ['households', householdId, 'catalog', 'search', query] as const,
  },
};

/** The household a household-scoped query key belongs to, or null for any other key. */
export function householdIdOfKey(queryKey: readonly unknown[]): string | null {
  const [group, id] = queryKey;
  return group === 'households' && typeof id === 'string' && id !== 'list' ? id : null;
}

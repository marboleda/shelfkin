// The app's one source of the current household.
//
// `CurrentHouseholdProvider` (in the root layout) loads `GET /households` for
// the signed-in user and picks the current one: the household last selected on
// this device (remembered per user, see storage.ts), or else the first by name.
// Screens read it with `useCurrentHousehold()` and switch with
// `useHouseholds().switchTo(id)`; always by ID, never by name.
//
// Every household-scoped query uses a key from `useHouseholdKeys()`, which all
// start with `['households', <current ID>]`. Switching changes the key, so a
// screen never shows the previous household's data under the new household's
// name (do not use `placeholderData: keepPreviousData` on these queries).
//
// When a household-scoped request gets a 404, the list is reloaded. If the
// current household is gone (the user was removed, or it was deleted), the
// app falls back to the first household (or onboarding when none are left) and
// shows "You no longer have access to <name>".

import { useQuery, useQueryClient } from '@tanstack/react-query';
import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from 'react';

import { isApiError } from '@/api/errors';
import { householdIdOfKey, queryKeys } from '@/api/query-keys';
import { useAuth } from '@/auth/session';

import { fetchHouseholds, withHousehold, type Household, type HouseholdRole } from './households';
import { loadRememberedHousehold, saveRememberedHousehold } from './storage';

export type CurrentHousehold = {
  id: string;
  name: string;
  role: HouseholdRole;
};

export type HouseholdsStatus =
  /** Nobody is signed in. */
  | { status: 'signedOut' }
  /** The list, or the remembered household, is still loading. */
  | { status: 'loading' }
  /** The list could not be loaded. Never treated as "no households". */
  | { status: 'error'; error: unknown; retry: () => void; retrying: boolean }
  /** Loaded. `current` is null only when the user has no households. */
  | { status: 'ready'; households: Household[]; current: CurrentHousehold | null };

export type Notice = { id: number; message: string };

type HouseholdsContextValue = {
  state: HouseholdsStatus;
  /** Makes the household with this ID current and remembers it on this device. */
  switchTo: (householdId: string) => void;
  /** Adds a household the user just created or joined to the list and makes it current. */
  adopt: (household: Household) => void;
  /** Reloads the list after a 404 for `householdId`, and switches away if it is gone. */
  reportHouseholdNotFound: (householdId: string) => Promise<void>;
  notice: Notice | null;
  showNotice: (message: string) => void;
  dismissNotice: () => void;
};

const HouseholdsContext = createContext<HouseholdsContextValue | null>(null);

type Selection = { uid: string; householdId: string | null };

function toCurrent(household: Household): CurrentHousehold {
  return { id: household.id, name: household.name, role: household.role };
}

/** Whether an error means the household (or something in it) was not found. */
function isNotFound(error: unknown): boolean {
  return isApiError(error) && error.status === 404;
}

export function CurrentHouseholdProvider({ children }: { children: ReactNode }) {
  const auth = useAuth();
  const uid = auth.status === 'signedIn' ? auth.user.uid : null;
  const queryClient = useQueryClient();

  const list = useQuery({
    queryKey: queryKeys.households.list(),
    queryFn: ({ signal }) => fetchHouseholds(signal),
    enabled: uid !== null,
  });

  // The selected household, restored from the device for each user. A
  // selection for another user (after signing out) is ignored.
  const [selection, setSelection] = useState<Selection | null>(null);
  useEffect(() => {
    if (!uid) return;
    let cancelled = false;
    loadRememberedHousehold(uid).then((householdId) => {
      if (cancelled) return;
      // Keep a choice made while storage was being read.
      setSelection((previous) => (previous?.uid === uid ? previous : { uid, householdId }));
    });
    return () => {
      cancelled = true;
    };
  }, [uid]);
  const selectionLoaded = uid !== null && selection?.uid === uid;
  const selectedId = selectionLoaded ? selection.householdId : null;

  // A household just created or joined, merged into the list until the list
  // itself has it, so it is current in the same render the user's choice is
  // made (the query cache tells its observers a moment later).
  const [adopted, setAdopted] = useState<{ uid: string; household: Household } | null>(null);
  const adoptedHousehold = adopted && adopted.uid === uid ? adopted.household : null;
  const households = useMemo(() => {
    if (!list.data) return undefined;
    if (adoptedHousehold && !list.data.some((item) => item.id === adoptedHousehold.id)) {
      return withHousehold(list.data, adoptedHousehold);
    }
    return list.data;
  }, [list.data, adoptedHousehold]);

  const currentHousehold = useMemo(() => {
    if (!households || !selectionLoaded) return null;
    return households.find((item) => item.id === selectedId) ?? households[0] ?? null;
  }, [households, selectionLoaded, selectedId]);
  const current = useMemo(
    () => (currentHousehold ? toCurrent(currentHousehold) : null),
    // Recreated only when what it exposes changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [currentHousehold?.id, currentHousehold?.name, currentHousehold?.role],
  );

  const switchTo = useCallback(
    (householdId: string) => {
      if (!uid) return;
      setSelection({ uid, householdId });
      void saveRememberedHousehold(uid, householdId);
    },
    [uid],
  );

  // The remembered household is no longer in the list: remember the fallback
  // on the device, so the next start opens it too.
  useEffect(() => {
    if (uid && current && selectionLoaded && selectedId !== current.id) {
      void saveRememberedHousehold(uid, current.id);
    }
  }, [uid, current, selectionLoaded, selectedId]);

  const adopt = useCallback(
    (household: Household) => {
      if (!uid) return;
      setAdopted({ uid, household });
      queryClient.setQueryData<Household[]>(queryKeys.households.list(), (previous) =>
        withHousehold(previous, household),
      );
      switchTo(household.id);
    },
    [uid, queryClient, switchTo],
  );

  // Messages belong to the user they were shown to.
  const [noticeState, setNotice] = useState<(Notice & { uid: string | null }) | null>(null);
  const notice = noticeState && noticeState.uid === uid ? noticeState : null;
  const noticeId = useRef(0);
  const showNotice = useCallback(
    (message: string) => {
      noticeId.current += 1;
      setNotice({ id: noticeId.current, message, uid });
    },
    [uid],
  );
  const dismissNotice = useCallback(() => setNotice(null), []);

  const currentRef = useRef(current);
  useEffect(() => {
    currentRef.current = current;
  }, [current]);
  const checking = useRef(new Set<string>());
  const reportHouseholdNotFound = useCallback(
    async (householdId: string) => {
      const lost = currentRef.current;
      if (!lost || lost.id !== householdId || checking.current.has(householdId)) return;
      checking.current.add(householdId);
      try {
        let fresh: Household[];
        try {
          fresh = await queryClient.fetchQuery({
            queryKey: queryKeys.households.list(),
            queryFn: ({ signal }) => fetchHouseholds(signal),
            staleTime: 0,
          });
        } catch {
          // Could not check; leave things as they are.
          return;
        }
        // Still a member: the 404 was for something inside the household, such as a deleted entry.
        if (fresh.some((item) => item.id === householdId)) return;
        // The list no longer has it, so the current household falls back to
        // the first one (or onboarding). Forget everything cached for it.
        setAdopted((previous) => (previous?.household.id === householdId ? null : previous));
        queryClient.removeQueries({ queryKey: queryKeys.households.detail(householdId) });
        showNotice(`You no longer have access to ${lost.name}`);
      } finally {
        checking.current.delete(householdId);
      }
    },
    [queryClient, showNotice],
  );

  // Watch every household-scoped query and mutation for a 404, and stop
  // merging an adopted household once the list itself has it.
  useEffect(() => {
    const unsubscribeQueries = queryClient.getQueryCache().subscribe((event) => {
      if (event.type !== 'updated') return;
      // Only after a real fetch: `adopt` itself writes the list with setQueryData (`manual`).
      if (event.action.type === 'success' && !event.action.manual && event.query.queryKey[1] === 'list') {
        const data = event.query.state.data as Household[] | undefined;
        setAdopted((previous) => (previous && data?.some((item) => item.id === previous.household.id) ? null : previous));
        return;
      }
      if (event.action.type !== 'error' || !isNotFound(event.action.error)) return;
      const householdId = householdIdOfKey(event.query.queryKey);
      if (householdId) void reportHouseholdNotFound(householdId);
    });
    const unsubscribeMutations = queryClient.getMutationCache().subscribe((event) => {
      if (event.type !== 'updated' || event.action.type !== 'error' || !isNotFound(event.action.error)) return;
      const householdId = householdIdOfKey(event.mutation.options.mutationKey ?? []);
      if (householdId) void reportHouseholdNotFound(householdId);
    });
    return () => {
      unsubscribeQueries();
      unsubscribeMutations();
    };
  }, [queryClient, reportHouseholdNotFound]);


  const { refetch, isFetching } = list;
  const state: HouseholdsStatus = useMemo(() => {
    if (!uid) return { status: 'signedOut' };
    if (households && selectionLoaded) return { status: 'ready', households, current };
    if (list.isError) {
      return { status: 'error', error: list.error, retry: () => void refetch(), retrying: isFetching };
    }
    return { status: 'loading' };
  }, [uid, households, selectionLoaded, current, list.isError, list.error, refetch, isFetching]);

  const value = useMemo(
    () => ({ state, switchTo, adopt, reportHouseholdNotFound, notice, showNotice, dismissNotice }),
    [state, switchTo, adopt, reportHouseholdNotFound, notice, showNotice, dismissNotice],
  );

  return <HouseholdsContext.Provider value={value}>{children}</HouseholdsContext.Provider>;
}

/** The household list and the actions on it. */
export function useHouseholds(): HouseholdsContextValue {
  const value = useContext(HouseholdsContext);
  if (!value) throw new Error('useHouseholds must be used inside CurrentHouseholdProvider');
  return value;
}

/**
 * The current household. Only for screens inside the signed-in shell, which
 * the household gate renders only once there is one.
 */
export function useCurrentHousehold(): CurrentHousehold {
  const { state } = useHouseholds();
  if (state.status !== 'ready' || !state.current) {
    throw new Error('useCurrentHousehold is only available once a current household is loaded');
  }
  return state.current;
}

/** The current household, or null while loading, signed out or with no households. */
export function useOptionalCurrentHousehold(): CurrentHousehold | null {
  const { state } = useHouseholds();
  return state.status === 'ready' ? state.current : null;
}

/** Query keys for the current household's data, from the key factory. */
export function useHouseholdKeys() {
  const { id } = useCurrentHousehold();
  return useMemo(
    () => ({
      householdId: id,
      detail: () => queryKeys.households.detail(id),
      pantry: () => queryKeys.households.pantry(id),
      members: () => queryKeys.households.members(id),
      invites: () => queryKeys.households.invites(id),
      catalogSearch: (query: string) => queryKeys.households.catalogSearch(id, query),
    }),
    [id],
  );
}

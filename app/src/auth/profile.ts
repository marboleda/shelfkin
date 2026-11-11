// The signed-in user's Shelfkin profile (`GET /me`, `PATCH /me`).

import { useMutation, useQuery, useQueryClient, type QueryClient } from '@tanstack/react-query';
import { useEffect } from 'react';

import { apiClient, callApi } from '@/api/client';
import { isApiError } from '@/api/errors';
import { queryKeys } from '@/api/query-keys';
import type { components } from '@/api/schema';

import { clearPendingProfile, loadPendingProfile } from './pending-profile';
import { useAuth, type AuthUser } from './session';
import { deviceTimeZone } from './timezone';

export type Me = components['schemas']['User'];
type UpdateMe = components['schemas']['UpdateMe'];

function fetchMe(signal?: AbortSignal): Promise<Me> {
  return callApi(() => apiClient.GET('/me', { signal }));
}

function patchMe(body: UpdateMe): Promise<Me> {
  return callApi(() => apiClient.PATCH('/me', { body }));
}

function sameEmail(a: string | null, b: string | null): boolean {
  return !!a && !!b && a.trim().toLowerCase() === b.trim().toLowerCase();
}

/**
 * Brings the API's profile up to date after a sign-in or app start:
 *
 * - A name kept from sign-up is sent with the device's time zone, and forgotten
 *   once the API has it. If the request fails it stays, and the next start sends it again.
 * - Otherwise `GET /me`, and if the stored time zone differs from the device's,
 *   `PATCH /me` with the new one (so expiry reminders follow a user who travels).
 */
export async function syncProfile(queryClient: QueryClient, user: AuthUser): Promise<Me> {
  const timezone = deviceTimeZone();
  const pending = await loadPendingProfile();

  if (pending && sameEmail(pending.email, user.email)) {
    let me: Me;
    try {
      me = await patchMe({ displayName: pending.displayName, ...(timezone ? { timezone } : {}) });
    } catch (error) {
      if (isApiError(error) && error.code === 'invalid_timezone') {
        me = await patchMe({ displayName: pending.displayName });
      } else {
        if (isApiError(error) && error.code === 'invalid_display_name') {
          // Retrying cannot help; keep the name the API chose.
          await clearPendingProfile();
        }
        throw error;
      }
    }
    await clearPendingProfile();
    queryClient.setQueryData(queryKeys.me(), me);
    return me;
  }

  const me = await queryClient.fetchQuery({
    queryKey: queryKeys.me(),
    queryFn: ({ signal }) => fetchMe(signal),
    staleTime: 0,
  });
  if (timezone && me.timezone !== timezone) {
    const updated = await patchMe({ timezone });
    queryClient.setQueryData(queryKeys.me(), updated);
    return updated;
  }
  return me;
}

/** Runs `syncProfile` after each sign-in and on each app start while signed in. Renders nothing. */
export function ProfileSync() {
  const auth = useAuth();
  const queryClient = useQueryClient();
  const user = auth.status === 'signedIn' ? auth.user : null;
  const uid = user?.uid;
  const email = user?.email ?? null;

  useEffect(() => {
    if (!uid) return;
    syncProfile(queryClient, { uid, email }).catch((error) => {
      // Not shown: the name stays queued and is sent again on the next start.
      console.warn('Profile sync failed', error);
    });
  }, [queryClient, uid, email]);

  return null;
}

/** `GET /me`: the signed-in user's profile. */
export function useMeQuery() {
  return useQuery({
    queryKey: queryKeys.me(),
    queryFn: ({ signal }) => fetchMe(signal),
  });
}

/** `PATCH /me` with a new display name. */
export function useUpdateNameMutation() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (displayName: string) => patchMe({ displayName: displayName.trim() }),
    onSuccess: (me) => {
      queryClient.setQueryData(queryKeys.me(), me);
    },
  });
}

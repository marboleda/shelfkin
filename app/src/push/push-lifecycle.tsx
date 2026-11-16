// Keeps push notifications working while the app runs. Rendered once in the
// root layout; renders nothing.

import { useQueryClient, type QueryClient } from '@tanstack/react-query';
import * as Notifications from 'expo-notifications';
import { useEffect } from 'react';

import { apiClient, callApi } from '@/api/client';
import { queryKeys } from '@/api/query-keys';
import type { components } from '@/api/schema';
import { addBeforeSignOutTask, getAuthState, useAuth } from '@/auth/session';

import { ensureAndroidChannel, isNativeApp } from './device';
import { registerOnStart, unregisterBeforeSignOut } from './registration';

type Me = components['schemas']['User'];

/** How old the cached profile may be for the start-up check (ProfileSync has usually just fetched it). */
const PROFILE_MAX_AGE_MS = 30_000;

if (isNativeApp()) {
  // A reminder that arrives while the app is open is shown as a banner, not dropped.
  Notifications.setNotificationHandler({
    handleNotification: async () => ({
      shouldShowBanner: true,
      shouldShowList: true,
      shouldPlaySound: true,
      shouldSetBadge: false,
    }),
  });
}

/** The signed-in user's profile: the cached one if recent, else `GET /me` (shared with any request in flight). */
function currentProfile(queryClient: QueryClient): Promise<Me> {
  return queryClient.fetchQuery({
    queryKey: queryKeys.me(),
    queryFn: ({ signal }) => callApi(() => apiClient.GET('/me', { signal })),
    staleTime: PROFILE_MAX_AGE_MS,
  });
}

/**
 * On iOS and Android:
 * - Creates the Android "Expiry reminders" channel.
 * - On each app start and sign-in, registers this device's token again when
 *   reminders are on for the account and this device, and permission is granted.
 * - Registers again when the OS hands the app a new push token.
 * - Deletes this device's token before every sign-out.
 */
export function PushLifecycle() {
  const auth = useAuth();
  const uid = auth.status === 'signedIn' ? auth.user.uid : null;
  const queryClient = useQueryClient();

  useEffect(() => {
    if (!isNativeApp()) return;
    ensureAndroidChannel().catch((error) => console.warn('Could not create the notification channel', error));
    return addBeforeSignOutTask(async () => {
      const state = getAuthState();
      if (state.status === 'signedIn') await unregisterBeforeSignOut(state.user.uid);
    });
  }, []);

  useEffect(() => {
    if (!uid || !isNativeApp()) return;
    let active = true;

    const register = async () => {
      const me = await currentProfile(queryClient);
      const state = getAuthState();
      if (!active || state.status !== 'signedIn' || state.user.uid !== uid) return;
      await registerOnStart(uid, me);
    };

    register().catch((error) => {
      // Tried again on the next start.
      console.warn('Could not register this device for reminders', error);
    });
    const subscription = Notifications.addPushTokenListener(() => {
      register().catch((error) => console.warn('Could not register the new push token', error));
    });
    return () => {
      active = false;
      subscription.remove();
    };
  }, [uid, queryClient]);

  return null;
}

// Tapping an expiry reminder opens Pantry for the household it is about (#22
// sends `data.householdId`) and makes that household current.

import * as Notifications from 'expo-notifications';
import { useRouter } from 'expo-router';
import { useCallback, useEffect } from 'react';

import { useHouseholds } from '@/household/current-household';

import { isNativeApp } from './device';

/** The prefix of a reminder's title before the household name (see `Title` in api/internal/expiry/job.go). */
const TITLE_PREFIX = 'Expiring soon in ';

export type ReminderTarget = {
  /** Identifies the tap, so one tap is handled once. */
  id: string;
  householdId: string;
  /** The household's name from the reminder's title, for the "no longer have access" message. */
  householdName: string | null;
};

/** What a tapped notification points at, or null for one that is not an expiry reminder. */
export function reminderTarget(response: Notifications.NotificationResponse): ReminderTarget | null {
  if (response.actionIdentifier !== Notifications.DEFAULT_ACTION_IDENTIFIER) return null;
  const { request } = response.notification;
  const householdId = (request.content.data as Record<string, unknown> | null | undefined)?.householdId;
  if (typeof householdId !== 'string' || householdId === '') return null;
  const title = request.content.title ?? '';
  const householdName = title.startsWith(TITLE_PREFIX) ? title.slice(TITLE_PREFIX.length).trim() || null : null;
  return { id: request.identifier, householdId, householdName };
}

/** Taps already handled in this app run. */
const handled = new Set<string>();

/**
 * Opens tapped reminders. Used once, in the signed-in shell's layout, which
 * renders only once the user is signed in with a household loaded. A tap that
 * opened the app while signed out is still waiting there (Expo keeps the last
 * response), so it opens right after sign-in.
 */
export function useOpenTappedReminders() {
  const router = useRouter();
  const { state, switchTo, showNotice } = useHouseholds();
  const households = state.status === 'ready' ? state.households : null;

  const open = useCallback(
    (target: ReminderTarget) => {
      if (!households || handled.has(target.id)) return;
      handled.add(target.id);
      if (households.some((household) => household.id === target.householdId)) {
        switchTo(target.householdId);
      } else {
        showNotice(`You no longer have access to ${target.householdName ?? 'that household'}`);
      }
      router.navigate('/');
      Notifications.clearLastNotificationResponseAsync().catch(() => {});
    },
    [households, router, showNotice, switchTo],
  );

  useEffect(() => {
    if (!isNativeApp() || !households) return;
    // A tap that opened the app from closed, or that arrived before this mounted.
    const last = Notifications.getLastNotificationResponse();
    const lastTarget = last ? reminderTarget(last) : null;
    if (lastTarget) open(lastTarget);
    // Taps while the app is running or in the background.
    const subscription = Notifications.addNotificationResponseReceivedListener((response) => {
      const target = reminderTarget(response);
      if (target) open(target);
    });
    return () => subscription.remove();
  }, [households, open]);
}

/** Test only: forgets which taps were handled. */
export function resetHandledRemindersForTests() {
  handled.clear();
}

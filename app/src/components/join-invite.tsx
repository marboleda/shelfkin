// The invite link screen, /join/<code> (#33), for a signed-in user. The route
// guard (SignedInForInvite) sends signed-out users to /sign-in first.

import Head from 'expo-router/head';
import { useRouter } from 'expo-router';
import { useEffect, useRef } from 'react';
import { ActivityIndicator, Text, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { isApiError } from '@/api/errors';
import { useHouseholds } from '@/household/current-household';
import { joinErrorMessage } from '@/household/households';
import { useJoinHouseholdMutation } from '@/household/mutations';
import { getAuthState } from '@/auth/session';
import { clearPendingInvite, releaseKeptPendingInvite, savePendingInvite } from '@/household/pending-invite';
import { useTheme, useThemedStyles, type Theme } from '@/theme';

import { Button } from './button';
import { FormMessage } from './form-message';
import { JoinHouseholdForm } from './household-forms';
import { PressableLink } from './pressable-link';
import { documentTitle, Screen } from './screen';

export const JOINING_TITLE = 'Joining…';
export const JOIN_FAILED_TITLE = "Couldn't join the household";
export const INCOMPLETE_LINK_TITLE = 'This invite link is incomplete';

// The only join errors that are about the invite itself, each with its status.
const DEFINITE_JOIN_ERRORS = new Map<string, number>([
  ['bad_request', 400],
  ['invite_not_found', 404],
  ['invite_revoked', 410],
  ['invite_expired', 410],
]);

/**
 * A definite answer about the invite, which trying again will not change. Only
 * these clear the pending code: 400 `bad_request`, 404 `invite_not_found`,
 * 410 `invite_revoked` and 410 `invite_expired`, each with its own status
 * (#26: branch on the error `code`, never on the status alone).
 *
 * Everything else is about the session, the network or the server, and keeps
 * the code with Retry: a 401 means the session ended or the token could not be
 * refreshed, so signing in again should still join; a 429 comes from Cloud Run
 * or the front end when no instance is free. Likewise 403, any other 4xx or
 * code, 5xx, a body that is not JSON and a network error.
 */
export function isDefiniteJoinError(error: unknown): boolean {
  if (!isApiError(error) || error.kind !== 'http' || error.code === undefined) return false;
  const status = DEFINITE_JOIN_ERRORS.get(error.code);
  return status !== undefined && status === error.status;
}

function isUnauthorized(error: unknown): boolean {
  return isApiError(error) && error.status === 401;
}

/**
 * Sends `POST /invites/join` once when it opens, with the code as it appears in
 * the link (the API ignores case, spaces and dashes). On success the household
 * becomes current and Pantry replaces this screen, with "You joined <name>" or
 * "You're already a member of <name>". A definite error offers Pantry (or
 * onboarding for a user with no household); any other error keeps the code
 * and offers Retry. A 401 saves the code so that it survives the sign-out of
 * an ended session, and signing in again opens this screen and joins.
 */
export function JoinInviteScreen({ code }: { code: string }) {
  const styles = useThemedStyles(createStyles);
  const router = useRouter();
  const join = useJoinHouseholdMutation();
  // A ref, not state: it is set before React can render or run the effect again
  // (including the second effect run in development's Strict Mode).
  const started = useRef(false);
  const mounted = useRef(false);

  function send() {
    // A new attempt: an earlier 401's mark no longer applies.
    releaseKeptPendingInvite();
    // mutateAsync rather than mutate's callbacks: those are skipped once the
    // screen unmounts, which an ended session's sign-out can do before the
    // error arrives, and the 401 save below must always happen.
    join.mutateAsync(code).then(
      () => {
        void clearPendingInvite();
        // Replaced, so Back does not return here or join again.
        if (mounted.current) router.replace('/');
      },
      (error: unknown) => {
        if (isDefiniteJoinError(error)) void clearPendingInvite();
        else if (isUnauthorized(error) && (mounted.current || getAuthState().status === 'signedOut')) {
          // Kept for the sign-out of the session this 401 may have ended. Not
          // when the user already left this screen while still signed in.
          void savePendingInvite(code, { keepOnEndedSession: true });
        }
      },
    );
  }

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      // Left while signed in (not an ended session): forget the 401's mark.
      releaseKeptPendingInvite();
    };
  }, []);

  useEffect(() => {
    if (started.current) return;
    started.current = true;
    send();
    // Once per screen; Retry sends again.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (join.isError && isDefiniteJoinError(join.error)) {
    return <JoinFailed message={joinErrorMessage(join.error)} code={code} offerCode={isNotFound(join.error)} />;
  }
  if (join.isError) {
    // Not about the invite (network, 401, 429, 5xx…): the code is kept, and Retry sends it again.
    return (
      <Screen title={JOIN_FAILED_TITLE}>
        <View testID="join-error" style={styles.body}>
          <FormMessage tone="error" message={joinErrorMessage(join.error)} />
          <Button label="Retry" onPress={send} />
        </View>
      </Screen>
    );
  }
  return <Joining />;
}

function isNotFound(error: unknown): boolean {
  return isApiError(error) && error.code === 'invite_not_found';
}

function Joining() {
  const styles = useThemedStyles(createStyles);
  const { colors } = useTheme();
  const insets = useSafeAreaInsets();
  return (
    <View
      testID="joining"
      aria-busy
      style={[styles.joining, { paddingTop: insets.top, paddingBottom: insets.bottom }]}
    >
      <Head>
        <title>{documentTitle(JOINING_TITLE)}</title>
      </Head>
      <ActivityIndicator size="large" color={colors.primary} />
      <Text role="heading" aria-level={1} style={styles.joiningText}>
        {JOINING_TITLE}
      </Text>
    </View>
  );
}

type JoinFailedProps = { message: string; code: string; offerCode: boolean };

/** Not found, expired or revoked. A code that matched nothing can be corrected with the shared join form. */
function JoinFailed({ message, code, offerCode }: JoinFailedProps) {
  const styles = useThemedStyles(createStyles);
  const router = useRouter();
  return (
    <Screen title={JOIN_FAILED_TITLE}>
      <View testID="join-error" style={styles.body}>
        <FormMessage tone="error" message={message} />
        {offerCode ? <JoinHouseholdForm initialCode={code} onJoined={() => router.replace('/')} /> : null}
        <LeaveButton />
      </View>
    </Screen>
  );
}

/** "Go to Pantry" for a user with a household, else "Create or join a household" (onboarding). */
function LeaveButton() {
  const router = useRouter();
  const { state } = useHouseholds();
  const hasNone = state.status === 'ready' && state.households.length === 0;
  return hasNone ? (
    <Button label="Create or join a household" variant="secondary" onPress={() => router.replace('/onboarding')} />
  ) : (
    <Button label="Go to Pantry" variant="secondary" onPress={() => router.replace('/')} />
  );
}

/** /join/ with no code. */
export function IncompleteInviteScreen() {
  const styles = useThemedStyles(createStyles);
  return (
    <Screen title={INCOMPLETE_LINK_TITLE}>
      <PressableLink href="/" accessibilityRole="link" style={styles.link}>
        <Text style={styles.linkLabel}>Go to Pantry</Text>
      </PressableLink>
    </Screen>
  );
}

function createStyles(theme: Theme) {
  return {
    joining: {
      flex: 1,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      gap: theme.spacing.lg,
      backgroundColor: theme.colors.background,
    },
    link: { alignSelf: 'flex-start' as const },
    linkLabel: { ...theme.typography.label, color: theme.colors.primary },
    joiningText: { ...theme.typography.title, color: theme.colors.text },
    body: { gap: theme.spacing.md },
  };
}

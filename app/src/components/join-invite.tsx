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
import { clearPendingInvite } from '@/household/pending-invite';
import { useTheme, useThemedStyles, type Theme } from '@/theme';

import { Button } from './button';
import { FormMessage } from './form-message';
import { JoinHouseholdForm } from './household-forms';
import { PressableLink } from './pressable-link';
import { documentTitle, Screen } from './screen';

export const JOINING_TITLE = 'Joining…';
export const JOIN_FAILED_TITLE = "Couldn't join the household";
export const INCOMPLETE_LINK_TITLE = 'This invite link is incomplete';

/**
 * An error that will not go away by trying again (not found, expired, revoked
 * or another 4xx), as opposed to a network error or a 5xx.
 */
export function isDefiniteJoinError(error: unknown): boolean {
  return isApiError(error) && error.status !== undefined && error.status >= 400 && error.status < 500;
}

/**
 * Sends `POST /invites/join` once when it opens, with the code as it appears in
 * the link (the API ignores case, spaces and dashes). On success the household
 * becomes current and Pantry replaces this screen, with "You joined <name>" or
 * "You're already a member of <name>". A definite error offers Pantry (or
 * onboarding for a user with no household); a network error offers Retry.
 */
export function JoinInviteScreen({ code }: { code: string }) {
  const styles = useThemedStyles(createStyles);
  const router = useRouter();
  const join = useJoinHouseholdMutation();
  // A ref, not state: it is set before React can render or run the effect again
  // (including the second effect run in development's Strict Mode).
  const started = useRef(false);

  function send() {
    join.mutate(code, {
      onSuccess: () => {
        void clearPendingInvite();
        // Replaced, so Back does not return here or join again.
        router.replace('/');
      },
      onError: (error) => {
        if (isDefiniteJoinError(error)) void clearPendingInvite();
      },
    });
  }

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
    // A network error or a 5xx: the code is kept, and Retry sends it again.
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

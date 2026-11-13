import { Redirect } from 'expo-router';
import { useState, type ReactNode } from 'react';
import { ActivityIndicator, Pressable, Text, View } from 'react-native';

import { userMessage } from '@/api/errors';
import { signOut } from '@/auth/session';
import { useHouseholds } from '@/household/current-household';
import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

import { Button } from './button';
import { FormMessage } from './form-message';
import { Screen } from './screen';
import { ThemedText } from './themed-text';

type HouseholdGateProps = {
  /**
   * `household`: the signed-in shell, which needs a current household (users
   * with none go to /onboarding). `none`: onboarding, for users with no
   * household (users with one go to Pantry).
   */
  needs: 'household' | 'none';
  children: ReactNode;
};

/**
 * Renders its routes once the household list has loaded. Until then a loading
 * screen, so a user with households never sees onboarding flash first. If the
 * list cannot be loaded, an error with Retry, never onboarding (which would
 * invite the user to create a duplicate household).
 */
export function HouseholdGate({ needs, children }: HouseholdGateProps) {
  const { state } = useHouseholds();

  switch (state.status) {
    case 'signedOut':
      // The auth guard around this gate sends the user to /sign-in.
      return null;
    case 'loading':
      return <HouseholdsLoading />;
    case 'error':
      return <HouseholdsLoadError error={state.error} retry={state.retry} retrying={state.retrying} />;
    case 'ready':
      if (needs === 'household' && !state.current) return <Redirect href="/onboarding" />;
      if (needs === 'none' && state.current) return <Redirect href="/" />;
      return children;
  }
}

export function HouseholdsLoading() {
  const theme = useTheme();
  const styles = useThemedStyles(createStyles);
  return (
    <View testID="households-loading" aria-busy accessibilityLabel="Loading your households" style={styles.loading}>
      <ActivityIndicator size="large" color={theme.colors.primary} />
    </View>
  );
}

type LoadErrorProps = { error: unknown; retry: () => void; retrying: boolean };

function HouseholdsLoadError({ error, retry, retrying }: LoadErrorProps) {
  const styles = useThemedStyles(createStyles);
  const [signingOut, setSigningOut] = useState(false);

  return (
    <Screen title="Couldn't load your households">
      <View testID="households-error" style={styles.errorBody}>
        <FormMessage tone="error" message={userMessage(error)} />
        <Button label="Retry" busy={retrying} onPress={retry} />
        <ThemedText variant="muted">Signed in with the wrong account?</ThemedText>
        <Pressable
          role="button"
          aria-label="Sign out"
          disabled={signingOut}
          onPress={() => {
            setSigningOut(true);
            signOut().catch(() => setSigningOut(false));
          }}
          style={styles.signOut}
        >
          <Text style={styles.signOutLabel}>Sign out</Text>
        </Pressable>
      </View>
    </Screen>
  );
}

function createStyles(theme: Theme) {
  return {
    loading: {
      flex: 1,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      backgroundColor: theme.colors.background,
    },
    errorBody: { gap: theme.spacing.md },
    signOut: { alignSelf: 'flex-start' as const, minHeight: MIN_TOUCH_TARGET, justifyContent: 'center' as const },
    signOutLabel: { ...theme.typography.label, color: theme.colors.primary },
  };
}

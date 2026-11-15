import Feather from '@expo/vector-icons/Feather';
import { useState } from 'react';
import { ActivityIndicator, Pressable, Text, View } from 'react-native';

import { useMeQuery } from '@/auth/profile';
import { signOut, useAuth } from '@/auth/session';
import { FormMessage } from '@/components/form-message';
import { CreateHouseholdForm, JoinHouseholdForm } from '@/components/household-forms';
import { Screen } from '@/components/screen';
import { ThemedText } from '@/components/themed-text';
import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

const ONBOARDING_TITLE = 'Set up your pantry';

/**
 * For signed-in users with no household (the setup layout sends anyone else to
 * Pantry): create a household or join one with an invite code. Either way the
 * household becomes current and the layout then opens Pantry.
 */
export default function OnboardingScreen() {
  const styles = useThemedStyles(createStyles);
  const me = useMeQuery();

  return (
    <Screen title={ONBOARDING_TITLE}>
      {me.data?.displayName ? <Text style={styles.greeting}>Hi, {me.data.displayName}</Text> : null}
      <ThemedText variant="muted">
        A household is the pantry you share. Start a new one, or join someone else&apos;s.
      </ThemedText>

      <View testID="onboarding-create" style={styles.card}>
        <CardHeading icon="home" title="Create a household" />
        <CreateHouseholdForm />
      </View>

      <View testID="onboarding-join" style={styles.card}>
        <CardHeading icon="user-plus" title="Join with an invite code" />
        <JoinHouseholdForm />
      </View>

      <SignOutRow />
    </Screen>
  );
}

function CardHeading({ icon, title }: { icon: 'home' | 'user-plus'; title: string }) {
  const styles = useThemedStyles(createStyles);
  const { colors } = useTheme();
  return (
    <View style={styles.cardHeading}>
      <View style={styles.cardIcon}>
        <Feather name={icon} size={20} color={colors.primary} aria-hidden accessible={false} />
      </View>
      <Text role="heading" aria-level={2} style={styles.cardTitle}>
        {title}
      </Text>
    </View>
  );
}

/** So someone signed in with the wrong account is not stuck here. */
function SignOutRow() {
  const styles = useThemedStyles(createStyles);
  const { colors } = useTheme();
  const auth = useAuth();
  const [signingOut, setSigningOut] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const email = auth.status === 'signedIn' ? auth.user.email : null;

  async function handleSignOut() {
    if (signingOut) return;
    setSigningOut(true);
    setError(null);
    try {
      await signOut();
    } catch {
      setError("Couldn't sign out. Please try again.");
      setSigningOut(false);
    }
  }

  return (
    <View style={styles.signOutRow}>
      {email ? <ThemedText variant="muted">Signed in as {email}</ThemedText> : null}
      <Pressable
        role="button"
        aria-label="Sign out"
        accessibilityState={{ disabled: signingOut, busy: signingOut }}
        disabled={signingOut}
        onPress={handleSignOut}
        style={styles.signOut}
      >
        <Text style={styles.signOutLabel}>Sign out</Text>
        {signingOut ? <ActivityIndicator size="small" color={colors.mutedText} /> : null}
      </Pressable>
      {error ? <FormMessage tone="error" message={error} /> : null}
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    greeting: { ...theme.typography.label, color: theme.colors.primary },
    card: {
      gap: theme.spacing.md + 2,
      padding: theme.spacing.lg + 2,
      backgroundColor: theme.colors.surface,
      borderWidth: 1,
      borderColor: theme.colors.border,
      borderRadius: theme.radii.lg,
    },
    cardHeading: { flexDirection: 'row' as const, alignItems: 'center' as const, gap: theme.spacing.sm + 2 },
    cardIcon: {
      width: 36,
      height: 36,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      borderRadius: theme.radii.md,
      backgroundColor: theme.colors.primarySubtle,
    },
    cardTitle: { ...theme.typography.title, fontSize: 18, color: theme.colors.text, flexShrink: 1 },
    signOutRow: { gap: theme.spacing.xs, alignItems: 'flex-start' as const },
    signOut: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.sm,
      minHeight: MIN_TOUCH_TARGET,
    },
    signOutLabel: { ...theme.typography.label, color: theme.colors.primary },
  };
}

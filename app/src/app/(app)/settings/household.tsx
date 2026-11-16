import { Text } from 'react-native';

import { HouseholdSettings } from '@/components/household-settings';
import { PressableLink } from '@/components/pressable-link';
import { Screen } from '@/components/screen';
import { useThemedStyles, type Theme } from '@/theme';

/** /settings/household: the current household's name, members and invites. Opened from Settings. */
export default function HouseholdSettingsScreen() {
  const styles = useThemedStyles(createStyles);
  return (
    <Screen
      title="Household"
      inShell
      accessory={
        <PressableLink
          href="/settings"
          accessibilityRole="link"
          accessibilityLabel="Back to Settings"
          hitSlop={8}
          style={styles.back}
        >
          <Text style={styles.backLabel}>‹ Settings</Text>
        </PressableLink>
      }
    >
      <HouseholdSettings />
    </Screen>
  );
}

function createStyles(theme: Theme) {
  return {
    back: {
      alignSelf: 'flex-start' as const,
      paddingVertical: theme.spacing.xs,
      paddingHorizontal: theme.spacing.sm,
      marginLeft: -theme.spacing.sm,
      borderRadius: theme.radii.sm,
    },
    backLabel: { ...theme.typography.label, color: theme.colors.primary },
  };
}

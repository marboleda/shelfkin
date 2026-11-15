import Feather from '@expo/vector-icons/Feather';
import { Text, View } from 'react-native';

import { useCurrentHousehold } from '@/household/current-household';
import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

import { HouseholdTile } from './household-switcher';
import { PressableLink } from './pressable-link';

/** The Household section on Settings: the current household's name and "Manage household". */
export function HouseholdSection() {
  const styles = useThemedStyles(createStyles);
  const { colors } = useTheme();
  const household = useCurrentHousehold();

  return (
    <View testID="household-section" style={styles.section}>
      <Text role="heading" aria-level={2} style={styles.sectionTitle}>
        Household
      </Text>
      <View style={styles.card}>
        <View style={styles.row}>
          <HouseholdTile name={household.name} size="large" />
          <View style={styles.rowText}>
            <Text testID="household-section-name" style={styles.name}>
              {household.name}
            </Text>
            <Text style={styles.role}>{household.role === 'owner' ? "You're the owner" : "You're a member"}</Text>
          </View>
        </View>
        <PressableLink
          href="/settings/household"
          testID="manage-household"
          accessibilityRole="link"
          accessibilityLabel="Manage household"
          style={[styles.row, styles.divider]}
        >
          <Text style={styles.link}>Manage household</Text>
          <Feather name="chevron-right" size={18} color={colors.mutedText} aria-hidden accessible={false} />
        </PressableLink>
      </View>
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    section: { gap: theme.spacing.sm },
    sectionTitle: { ...theme.typography.overline, color: theme.colors.mutedText, paddingHorizontal: theme.spacing.xs },
    card: {
      backgroundColor: theme.colors.surface,
      borderColor: theme.colors.border,
      borderWidth: 1,
      borderRadius: theme.radii.card,
      overflow: 'hidden' as const,
    },
    row: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.md,
      minHeight: Math.max(56, MIN_TOUCH_TARGET),
      paddingVertical: theme.spacing.sm,
      paddingHorizontal: theme.spacing.md + 2,
    },
    divider: { borderTopWidth: 1, borderTopColor: theme.colors.border },
    rowText: { flex: 1, minWidth: 0, gap: 2 },
    name: { ...theme.typography.label, fontSize: 17, color: theme.colors.text },
    role: { ...theme.typography.caption, color: theme.colors.mutedText },
    link: { ...theme.typography.label, fontSize: 16, flex: 1, color: theme.colors.text },
  };
}

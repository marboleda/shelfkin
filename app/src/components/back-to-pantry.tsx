import { Text } from 'react-native';

import { useThemedStyles, type Theme } from '@/theme';

import { PressableLink } from './pressable-link';

/** Visible back control for screens opened from Pantry. Always returns to Pantry. */
export function BackToPantry() {
  const styles = useThemedStyles(createStyles);
  return (
    <PressableLink
      href="/"
      accessibilityRole="link"
      accessibilityLabel="Back to Pantry"
      hitSlop={8}
      style={styles.button}
    >
      <Text style={styles.label}>‹ Back to Pantry</Text>
    </PressableLink>
  );
}

function createStyles(theme: Theme) {
  return {
    button: {
      alignSelf: 'flex-start' as const,
      paddingVertical: theme.spacing.xs,
      paddingHorizontal: theme.spacing.sm,
      marginLeft: -theme.spacing.sm,
      borderRadius: theme.radii.sm,
    },
    label: {
      ...theme.typography.label,
      color: theme.colors.primary,
    },
  };
}

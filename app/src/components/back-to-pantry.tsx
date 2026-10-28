import { Link } from 'expo-router';
import { Pressable, Text } from 'react-native';

import { useThemedStyles, type Theme } from '@/theme';

/** Visible back control for screens opened from Pantry. Always returns to Pantry. */
export function BackToPantry() {
  const styles = useThemedStyles(createStyles);
  return (
    <Link href="/" asChild>
      <Pressable
        accessibilityRole="link"
        accessibilityLabel="Back to Pantry"
        hitSlop={8}
        style={({ pressed }) => [styles.button, pressed && styles.pressed]}
      >
        <Text style={styles.label}>‹ Back to Pantry</Text>
      </Pressable>
    </Link>
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
    pressed: {
      backgroundColor: theme.colors.primarySubtle,
    },
    label: {
      ...theme.typography.label,
      color: theme.colors.primary,
    },
  };
}

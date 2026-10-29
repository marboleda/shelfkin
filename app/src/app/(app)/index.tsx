import { Text, View } from 'react-native';

import { PressableLink } from '@/components/pressable-link';
import { Screen } from '@/components/screen';
import { ThemedText } from '@/components/themed-text';
import { useThemedStyles, type Theme } from '@/theme';

export default function PantryScreen() {
  const styles = useThemedStyles(createStyles);

  return (
    <Screen title="Pantry" inShell>
      <ThemedText variant="muted">Your household&apos;s items will appear here.</ThemedText>

      <PressableLink
        href="/add"
        testID="add-item-button"
        accessibilityRole="button"
        style={styles.primaryButton}
      >
        <Text style={styles.primaryButtonLabel}>Add item</Text>
      </PressableLink>

      <PressableLink href="/entries/sample" accessibilityRole="link" style={styles.row}>
        <View style={styles.rowText}>
          <ThemedText>Sample entry</ThemedText>
          <ThemedText variant="muted" style={styles.rowCaption}>
            Opens the entry detail placeholder
          </ThemedText>
        </View>
        <ThemedText variant="muted">›</ThemedText>
      </PressableLink>
    </Screen>
  );
}

function createStyles(theme: Theme) {
  return {
    primaryButton: {
      alignSelf: 'flex-start' as const,
      backgroundColor: theme.colors.primary,
      paddingVertical: theme.spacing.md,
      paddingHorizontal: theme.spacing.xl,
      borderRadius: theme.radii.pill,
    },
    primaryButtonLabel: {
      ...theme.typography.label,
      color: theme.colors.onPrimary,
    },
    row: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.md,
      padding: theme.spacing.lg,
      backgroundColor: theme.colors.surface,
      borderColor: theme.colors.border,
      borderWidth: 1,
      borderRadius: theme.radii.md,
    },
    rowText: {
      flex: 1,
    },
    rowCaption: {
      ...theme.typography.caption,
    },
  };
}

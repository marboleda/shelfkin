import { Text, View } from 'react-native';

import type { ExpiryLabel } from '@/pantry/format';
import { useThemedStyles, type Theme } from '@/theme';

/**
 * An expiry label from `expiryLabel()`. "Expires today/tomorrow/in N days" is a
 * chip in the theme's expiring-soon colours, "Expired Oct 25" a chip in the
 * danger colours; anything else is a plain caption (muted for out entries).
 * The words always say what the colour does. Shared by Pantry and Entry detail.
 */
export function ExpiryChip({ label }: { label: ExpiryLabel }) {
  const styles = useThemedStyles(createStyles);
  if (label.tone === 'soon' || label.tone === 'expired') {
    return (
      <View testID={`expiry-chip-${label.tone}`} style={[styles.chip, label.tone === 'soon' ? styles.soonChip : styles.expiredChip]}>
        <Text style={[styles.chipText, label.tone === 'soon' ? styles.soonText : styles.expiredText]}>{label.text}</Text>
      </View>
    );
  }
  return (
    <Text testID={`expiry-${label.tone}`} style={styles.plain}>
      {label.text}
    </Text>
  );
}

function createStyles(theme: Theme) {
  return {
    chip: {
      alignSelf: 'flex-start' as const,
      paddingHorizontal: theme.spacing.sm,
      paddingVertical: 2,
      borderRadius: theme.radii.pill,
    },
    soonChip: { backgroundColor: theme.colors.expiringSubtle },
    expiredChip: { backgroundColor: theme.colors.dangerSubtle },
    chipText: {
      ...theme.typography.caption,
      fontFamily: theme.typography.label.fontFamily,
      fontWeight: theme.typography.label.fontWeight,
    },
    soonText: { color: theme.colors.expiringSoon },
    expiredText: { color: theme.colors.danger },
    plain: { ...theme.typography.caption, color: theme.colors.mutedText },
  };
}

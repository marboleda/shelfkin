import { Text, View } from 'react-native';

import { levelLabel, type StockLevel } from '@/pantry/format';
import { useTheme, useThemedStyles, type Theme } from '@/theme';

/** How many of the three bars are filled at each level. */
const FILLED_BARS: Record<StockLevel, number> = { full: 3, half: 2, low: 1, out: 0 };

type LevelMeterProps = {
  level: StockLevel;
  /** Bigger bars, for Entry detail. */
  size?: 'small' | 'large';
  /** Hides the word, when the level is already said nearby (the meter is then only decoration). */
  hideLabel?: boolean;
};

/**
 * A stock level as three bars and its word ("Full", "Half", "Low", "Out"):
 * never colour alone. Low fills one bar in `levelLow`, the others use `primary`;
 * empty bars use `levelEmpty`. Shared by Pantry and Entry detail.
 */
export function LevelMeter({ level, size = 'small', hideLabel = false }: LevelMeterProps) {
  const theme = useTheme();
  const styles = useThemedStyles(createStyles);
  const filled = FILLED_BARS[level] ?? 0;
  const fill = level === 'low' ? theme.colors.levelLow : theme.colors.primary;
  const labelColor =
    level === 'out' ? theme.colors.mutedText : level === 'low' ? theme.colors.expiringSoon : level === 'full' ? theme.colors.primary : theme.colors.text;

  return (
    <View style={styles.root} testID="level-meter">
      <View style={styles.bars} aria-hidden importantForAccessibility="no-hide-descendants">
        {[0, 1, 2].map((index) => (
          <View
            key={index}
            style={[
              size === 'large' ? styles.barLarge : styles.bar,
              { backgroundColor: index < filled ? fill : theme.colors.levelEmpty },
            ]}
          />
        ))}
      </View>
      {hideLabel ? null : <Text style={[styles.label, { color: labelColor }]}>{levelLabel(level)}</Text>}
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    root: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.sm,
    },
    bars: {
      flexDirection: 'row' as const,
      gap: 3,
    },
    bar: { width: 6, height: 18, borderRadius: 2 },
    barLarge: { width: 8, height: 24, borderRadius: 2 },
    label: {
      ...theme.typography.caption,
      fontFamily: theme.typography.label.fontFamily,
      fontWeight: theme.typography.label.fontWeight,
      minWidth: 32,
    },
  };
}

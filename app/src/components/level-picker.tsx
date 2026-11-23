import { Pressable, Text, View } from 'react-native';

import { levelLabel, type StockLevel } from '@/pantry/format';
import { MIN_TOUCH_TARGET, useThemedStyles, type Theme } from '@/theme';

import { LevelMeter } from './level-meter';

export const STOCK_LEVELS: readonly StockLevel[] = ['full', 'half', 'low', 'out'];

type LevelPickerProps = {
  label?: string;
  value: StockLevel;
  onChange: (level: StockLevel) => void;
  disabled?: boolean;
  testID?: string;
};

/** The four stock levels (Full, Half, Low, Out) as a radio group, each with its level meter. */
export function LevelPicker({ label = 'How much?', value, onChange, disabled = false, testID = 'level-picker' }: LevelPickerProps) {
  const styles = useThemedStyles(createStyles);
  return (
    <View style={styles.field}>
      <Text style={styles.label}>{label}</Text>
      <View accessibilityRole="radiogroup" accessibilityLabel={label} style={styles.row} testID={testID}>
        {STOCK_LEVELS.map((level) => {
          const selected = level === value;
          return (
            <Pressable
              key={level}
              testID={`${testID}-${level}`}
              accessibilityRole="radio"
              accessibilityLabel={levelLabel(level)}
              accessibilityState={{ checked: selected, disabled }}
              aria-checked={selected}
              disabled={disabled}
              onPress={() => onChange(level)}
              style={[styles.option, selected ? styles.optionSelected : null]}
            >
              <LevelMeter level={level} />
            </Pressable>
          );
        })}
      </View>
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    field: { gap: theme.spacing.sm },
    label: { ...theme.typography.label, color: theme.colors.text },
    row: { flexDirection: 'row' as const, flexWrap: 'wrap' as const, gap: theme.spacing.sm },
    option: {
      flexGrow: 1,
      flexBasis: 70,
      minHeight: MIN_TOUCH_TARGET + 8,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      paddingHorizontal: theme.spacing.sm,
      borderRadius: theme.radii.md,
      borderWidth: 1,
      borderColor: theme.colors.border,
      backgroundColor: theme.colors.surface,
    },
    optionSelected: { borderColor: theme.colors.primary, backgroundColor: theme.colors.primarySubtle },
  };
}

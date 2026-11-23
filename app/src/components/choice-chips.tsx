import { Pressable, Text, View } from 'react-native';

import { MIN_TOUCH_TARGET, useThemedStyles, type Theme } from '@/theme';

export type ChoiceOption<T extends string | number> = { value: T; label: string };

type ChoiceChipsProps<T extends string | number> = {
  /** The group's visible label, also its accessible name. */
  label: string;
  options: readonly ChoiceOption<T>[];
  /** The selected value, or null when nothing is selected yet. */
  value: T | null;
  onChange: (value: T) => void;
  /** Shown under the chips, and announced, when set. */
  error?: string | null;
  disabled?: boolean;
  testID?: string;
};

/**
 * A single-choice group of chips, announced as a radio group: each chip is a
 * radio that says whether it is checked. It can start with nothing selected.
 * Selected chips use a `primarySubtle` background with a `primary` border.
 */
export function ChoiceChips<T extends string | number>({
  label,
  options,
  value,
  onChange,
  error,
  disabled = false,
  testID,
}: ChoiceChipsProps<T>) {
  const styles = useThemedStyles(createStyles);
  return (
    <View style={styles.field} testID={testID}>
      <Text style={styles.label}>{label}</Text>
      <View accessibilityRole="radiogroup" accessibilityLabel={label} style={styles.chips}>
        {options.map((option) => {
          const selected = option.value === value;
          return (
            <Pressable
              key={String(option.value)}
              testID={testID ? `${testID}-${option.value}` : undefined}
              accessibilityRole="radio"
              accessibilityLabel={option.label}
              accessibilityState={{ checked: selected, disabled }}
              aria-checked={selected}
              disabled={disabled}
              onPress={() => onChange(option.value)}
              style={[styles.chip, selected ? styles.chipSelected : null]}
            >
              <Text style={[styles.chipLabel, selected ? styles.chipLabelSelected : null]}>{option.label}</Text>
            </Pressable>
          );
        })}
      </View>
      {error ? (
        <Text role="alert" style={styles.error}>
          {error}
        </Text>
      ) : null}
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    field: { gap: theme.spacing.sm },
    label: { ...theme.typography.label, color: theme.colors.text },
    chips: { flexDirection: 'row' as const, flexWrap: 'wrap' as const, gap: theme.spacing.sm },
    chip: {
      minHeight: MIN_TOUCH_TARGET,
      paddingHorizontal: theme.spacing.lg,
      justifyContent: 'center' as const,
      borderRadius: theme.radii.pill,
      borderWidth: 1,
      borderColor: theme.colors.border,
      backgroundColor: theme.colors.surface,
    },
    chipSelected: { borderColor: theme.colors.primary, backgroundColor: theme.colors.primarySubtle },
    chipLabel: { ...theme.typography.label, color: theme.colors.text },
    chipLabelSelected: { color: theme.colors.primary },
    error: { ...theme.typography.caption, color: theme.colors.danger },
  };
}

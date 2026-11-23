import { useId, type ChangeEvent } from 'react';
import { Pressable, Text, View } from 'react-native';

import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

import type { DateFieldProps } from './date-field-props';

export type { DateFieldProps } from './date-field-props';

/**
 * The web version of `DateField` (see date-field.tsx): native date pickers do
 * not run in a browser, so this uses `<input type="date">`, whose value is
 * already `YYYY-MM-DD` (or '' when emptied).
 */
export function DateField({ label, value, onChange, error, hint, clearLabel, disabled = false, testID = 'date-field' }: DateFieldProps) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const id = useId();
  const messageId = `${id}-message`;

  return (
    <View style={styles.field}>
      <label htmlFor={id} style={{ ...theme.typography.label, color: theme.colors.text } as object}>
        {label}
      </label>
      <View style={styles.row}>
        <input
          id={id}
          type="date"
          data-testid={testID}
          value={value}
          disabled={disabled}
          aria-invalid={error ? true : undefined}
          aria-describedby={error || hint ? messageId : undefined}
          onChange={(event: ChangeEvent<HTMLInputElement>) => onChange(event.target.value)}
          style={{
            flex: 1,
            minHeight: 50,
            boxSizing: 'border-box',
            paddingLeft: theme.spacing.md + 2,
            paddingRight: theme.spacing.md,
            borderWidth: 1,
            borderStyle: 'solid',
            borderColor: error ? theme.colors.danger : theme.colors.border,
            borderRadius: theme.radii.md,
            backgroundColor: theme.colors.surface,
            color: theme.colors.text,
            colorScheme: theme.dark ? 'dark' : 'light',
            fontFamily: theme.typography.body.fontFamily,
            fontSize: theme.typography.body.fontSize,
          }}
        />
        {clearLabel && value ? (
          <Pressable
            testID={`${testID}-clear`}
            accessibilityRole="button"
            accessibilityLabel={clearLabel}
            disabled={disabled}
            onPress={() => onChange('')}
            style={styles.clear}
          >
            <Text style={styles.clearLabel}>Clear</Text>
          </Pressable>
        ) : null}
      </View>
      {error ? (
        <Text nativeID={messageId} role="alert" style={styles.error}>
          {error}
        </Text>
      ) : hint ? (
        <Text nativeID={messageId} style={styles.hint}>
          {hint}
        </Text>
      ) : null}
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    field: { gap: theme.spacing.xs + 2 },
    row: { flexDirection: 'row' as const, alignItems: 'center' as const, gap: theme.spacing.sm },
    clear: {
      minHeight: MIN_TOUCH_TARGET,
      minWidth: MIN_TOUCH_TARGET,
      paddingHorizontal: theme.spacing.md,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
    },
    clearLabel: { ...theme.typography.label, color: theme.colors.primary },
    error: { ...theme.typography.caption, color: theme.colors.danger },
    hint: { ...theme.typography.caption, color: theme.colors.mutedText },
  };
}

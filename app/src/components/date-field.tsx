import DateTimePicker, { DateTimePickerAndroid } from '@react-native-community/datetimepicker';
import { useState } from 'react';
import { Platform, Pressable, Text, View } from 'react-native';

import { todayOnDevice } from '@/pantry/dates';
import { formatCalendarDate } from '@/pantry/format';
import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

import { fromPickerDate, toPickerDate, type DateFieldProps } from './date-field-props';

export type { DateFieldProps } from './date-field-props';

/**
 * A labelled calendar date that takes and returns `YYYY-MM-DD` strings, with an
 * optional clear control. On iOS and Android the field is a button that opens
 * the platform's date picker (inline on iOS, a dialog on Android); on web,
 * date-field.web.tsx uses `<input type="date">` instead.
 */
export function DateField({ label, value, onChange, error, hint, clearLabel, disabled = false, testID = 'date-field' }: DateFieldProps) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const [open, setOpen] = useState(false);
  const shown = value ? formatCalendarDate(value, todayOnDevice()) : 'Not set';

  const openPicker = () => {
    if (Platform.OS === 'android') {
      DateTimePickerAndroid.open({
        value: toPickerDate(value),
        mode: 'date',
        onValueChange: (_event, date) => onChange(fromPickerDate(date)),
      });
      return;
    }
    setOpen((current) => !current);
  };

  return (
    <View style={styles.field}>
      <Text style={styles.label}>{label}</Text>
      <View style={styles.row}>
        <Pressable
          testID={testID}
          accessibilityRole="button"
          accessibilityLabel={`${label}, ${value ? shown : 'not set'}`}
          accessibilityHint={error ?? 'Opens a date picker'}
          accessibilityState={{ disabled, expanded: Platform.OS === 'ios' ? open : undefined }}
          disabled={disabled}
          onPress={openPicker}
          style={[styles.input, error ? styles.inputError : null]}
        >
          <Text style={value ? styles.value : styles.placeholder}>{shown}</Text>
        </Pressable>
        {clearLabel && value ? (
          <Pressable
            testID={`${testID}-clear`}
            accessibilityRole="button"
            accessibilityLabel={clearLabel}
            disabled={disabled}
            onPress={() => {
              setOpen(false);
              onChange('');
            }}
            style={styles.clear}
          >
            <Text style={styles.clearLabel}>Clear</Text>
          </Pressable>
        ) : null}
      </View>
      {open && Platform.OS !== 'android' ? (
        <View style={styles.picker}>
          <DateTimePicker
            testID={`${testID}-picker`}
            value={toPickerDate(value)}
            mode="date"
            display="inline"
            accentColor={theme.colors.primary}
            themeVariant={theme.dark ? 'dark' : 'light'}
            onValueChange={(_event, date) => onChange(fromPickerDate(date))}
          />
          <Pressable
            testID={`${testID}-done`}
            accessibilityRole="button"
            accessibilityLabel={`Done choosing ${label.toLowerCase()}`}
            onPress={() => setOpen(false)}
            style={styles.done}
          >
            <Text style={styles.clearLabel}>Done</Text>
          </Pressable>
        </View>
      ) : null}
      {error ? (
        <Text role="alert" style={styles.error}>
          {error}
        </Text>
      ) : hint ? (
        <Text style={styles.hint}>{hint}</Text>
      ) : null}
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    field: { gap: theme.spacing.xs + 2 },
    label: { ...theme.typography.label, color: theme.colors.text },
    row: { flexDirection: 'row' as const, alignItems: 'center' as const, gap: theme.spacing.sm },
    input: {
      flex: 1,
      minHeight: 50,
      justifyContent: 'center' as const,
      paddingHorizontal: theme.spacing.md + 2,
      borderWidth: 1,
      borderColor: theme.colors.border,
      borderRadius: theme.radii.md,
      backgroundColor: theme.colors.surface,
    },
    inputError: { borderColor: theme.colors.danger },
    value: { ...theme.typography.body, color: theme.colors.text },
    placeholder: { ...theme.typography.body, color: theme.colors.mutedText },
    clear: {
      minHeight: MIN_TOUCH_TARGET,
      minWidth: MIN_TOUCH_TARGET,
      paddingHorizontal: theme.spacing.md,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
    },
    clearLabel: { ...theme.typography.label, color: theme.colors.primary },
    picker: { gap: theme.spacing.xs },
    done: {
      alignSelf: 'flex-end' as const,
      minHeight: MIN_TOUCH_TARGET,
      paddingHorizontal: theme.spacing.md,
      justifyContent: 'center' as const,
    },
    error: { ...theme.typography.caption, color: theme.colors.danger },
    hint: { ...theme.typography.caption, color: theme.colors.mutedText },
  };
}

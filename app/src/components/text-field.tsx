import { forwardRef, useState } from 'react';
import { Pressable, Text, TextInput, View, type TextInputInstance, type TextInputProps } from 'react-native';

import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

type TextFieldProps = Omit<TextInputProps, 'style' | 'secureTextEntry'> & {
  label: string;
  /** Shown under the field, and announced, when set. */
  error?: string | null;
  /** A password field: hides the text and adds a Show / Hide control. */
  password?: boolean;
};

/** A labelled text input with its own error message underneath. */
export const TextField = forwardRef<TextInputInstance, TextFieldProps>(function TextField(
  { label, error, password = false, testID, ...inputProps },
  ref,
) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const [visible, setVisible] = useState(false);

  return (
    <View style={styles.field}>
      <Text style={styles.label}>{label}</Text>
      <View style={[styles.inputRow, error ? styles.inputRowError : null]}>
        <TextInput
          ref={ref}
          testID={testID}
          accessibilityLabel={label}
          accessibilityHint={error ?? undefined}
          placeholderTextColor={theme.colors.mutedText}
          secureTextEntry={password && !visible}
          style={styles.input}
          {...inputProps}
        />
        {password ? (
          <Pressable
            onPress={() => setVisible((value) => !value)}
            accessibilityRole="button"
            accessibilityLabel={visible ? 'Hide password' : 'Show password'}
            style={styles.toggle}
          >
            <Text style={styles.toggleLabel}>{visible ? 'Hide' : 'Show'}</Text>
          </Pressable>
        ) : null}
      </View>
      {error ? (
        <Text role="alert" style={styles.error}>
          {error}
        </Text>
      ) : null}
    </View>
  );
});

function createStyles(theme: Theme) {
  return {
    field: { gap: theme.spacing.xs + 2 },
    label: { ...theme.typography.label, color: theme.colors.text },
    inputRow: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      minHeight: 50,
      borderWidth: 1,
      borderColor: theme.colors.border,
      borderRadius: theme.radii.md,
      backgroundColor: theme.colors.surface,
    },
    inputRowError: { borderColor: theme.colors.danger },
    input: {
      ...theme.typography.body,
      flex: 1,
      minHeight: 48,
      paddingHorizontal: theme.spacing.md + 2,
      color: theme.colors.text,
    },
    toggle: {
      minHeight: MIN_TOUCH_TARGET,
      minWidth: MIN_TOUCH_TARGET,
      paddingHorizontal: theme.spacing.md,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
    },
    toggleLabel: { ...theme.typography.label, color: theme.colors.primary },
    error: { ...theme.typography.caption, color: theme.colors.danger },
  };
}

import type { ComponentRef, Ref } from 'react';
import { ActivityIndicator, Pressable, Text, type PressableProps } from 'react-native';

import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

type ButtonProps = Omit<PressableProps, 'style' | 'children'> & {
  label: string;
  /** Shows a spinner and disables the button. */
  busy?: boolean;
  variant?: 'primary' | 'secondary' | 'danger' | 'link';
  /** The pressable, e.g. to move keyboard focus to it. */
  ref?: Ref<ComponentRef<typeof Pressable>>;
};

/** A full-width primary, secondary or danger (destructive) action, or a text link-style button. */
export function Button({ label, busy = false, disabled, variant = 'primary', ...rest }: ButtonProps) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const inactive = busy || !!disabled;

  return (
    <Pressable
      accessibilityRole="button"
      accessibilityLabel={label}
      accessibilityState={{ disabled: inactive, busy }}
      aria-busy={busy}
      disabled={inactive}
      style={[styles.base, styles[variant], inactive && variant !== 'link' ? styles.inactive : null]}
      {...rest}
    >
      {busy ? (
        <ActivityIndicator
          testID="button-busy"
          size="small"
          color={
            variant === 'primary' ? theme.colors.onPrimary : variant === 'danger' ? theme.colors.background : theme.colors.primary
          }
        />
      ) : null}
      <Text style={styles[`${variant}Label`]}>{label}</Text>
    </Pressable>
  );
}

function createStyles(theme: Theme) {
  return {
    base: {
      minHeight: MIN_TOUCH_TARGET,
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      gap: theme.spacing.sm,
    },
    primary: {
      minHeight: 54,
      paddingHorizontal: theme.spacing.lg,
      borderRadius: theme.radii.card,
      backgroundColor: theme.colors.primary,
    },
    secondary: {
      minHeight: 54,
      paddingHorizontal: theme.spacing.lg,
      borderRadius: theme.radii.card,
      borderWidth: 1,
      borderColor: theme.colors.border,
      backgroundColor: theme.colors.surface,
    },
    danger: {
      minHeight: 54,
      paddingHorizontal: theme.spacing.lg,
      borderRadius: theme.radii.card,
      backgroundColor: theme.colors.danger,
    },
    link: {
      alignSelf: 'flex-start' as const,
      paddingHorizontal: theme.spacing.xs,
    },
    inactive: { opacity: 0.7 },
    primaryLabel: { ...theme.typography.label, fontSize: 16, color: theme.colors.onPrimary },
    secondaryLabel: { ...theme.typography.label, fontSize: 16, color: theme.colors.text },
    // The background colour reads on `danger` in both light and dark mode.
    dangerLabel: { ...theme.typography.label, fontSize: 16, color: theme.colors.background },
    linkLabel: { ...theme.typography.label, color: theme.colors.primary },
  };
}

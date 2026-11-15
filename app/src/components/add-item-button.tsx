import Feather from '@expo/vector-icons/Feather';
import { Text } from 'react-native';

import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

import { PressableLink } from './pressable-link';

/** Where Add item lives. */
export const ADD_ITEM_HREF = '/add';

type AddItemButtonProps = {
  /**
   * `inline`: a button in the page flow (the wide layout, next to the list).
   * `floating`: a pill floating over the bottom right of the list (phones).
   */
  placement?: 'inline' | 'floating';
  label?: string;
  testID?: string;
};

/** Opens Add item (`/add`). Pantry shows one at all times; its empty state shows another. */
export function AddItemButton({ placement = 'inline', label = 'Add item', testID = 'add-item-button' }: AddItemButtonProps) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();

  return (
    <PressableLink
      href={ADD_ITEM_HREF}
      testID={testID}
      accessibilityRole="button"
      accessibilityLabel={label}
      style={[styles.base, placement === 'floating' ? styles.floating : styles.inline]}
    >
      <Feather name="plus" size={20} color={theme.colors.onPrimary} aria-hidden />
      <Text style={styles.label}>{label}</Text>
    </PressableLink>
  );
}

function createStyles(theme: Theme) {
  return {
    base: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.sm,
      minHeight: MIN_TOUCH_TARGET,
      backgroundColor: theme.colors.primary,
    },
    inline: {
      alignSelf: 'flex-start' as const,
      paddingLeft: theme.spacing.md,
      paddingRight: theme.spacing.lg,
      borderRadius: theme.radii.md,
    },
    floating: {
      position: 'absolute' as const,
      right: theme.spacing.lg,
      bottom: theme.spacing.lg,
      minHeight: 56,
      paddingLeft: theme.spacing.lg,
      paddingRight: theme.spacing.xl,
      borderRadius: theme.radii.pill,
      // Lifts the button off the rows scrolling under it.
      elevation: 6,
      shadowColor: theme.colors.text,
      shadowOpacity: 0.18,
      shadowRadius: 12,
      shadowOffset: { width: 0, height: 6 },
    },
    label: {
      ...theme.typography.label,
      fontSize: 16,
      color: theme.colors.onPrimary,
    },
  };
}

import Feather from '@expo/vector-icons/Feather';
import { useEffect } from 'react';
import { Pressable, Text, View } from 'react-native';
import { useSafeAreaInsets } from 'react-native-safe-area-context';

import { useHouseholds } from '@/household/current-household';
import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

/** How long a household message stays up, in ms. It can also be dismissed. */
export const NOTICE_DURATION_MS = 6000;

/**
 * A short message over the top of the signed-in shell, such as "You joined
 * Home" or "You no longer have access to Home". Announced when it appears.
 */
export function HouseholdNotice() {
  const styles = useThemedStyles(createStyles);
  const { colors, spacing } = useTheme();
  const insets = useSafeAreaInsets();
  const { notice, dismissNotice } = useHouseholds();

  useEffect(() => {
    if (!notice) return;
    const timer = setTimeout(dismissNotice, NOTICE_DURATION_MS);
    return () => clearTimeout(timer);
  }, [notice, dismissNotice]);

  if (!notice) return null;
  return (
    <View pointerEvents="box-none" style={[styles.layer, { top: insets.top + spacing.md }]}>
      <View testID="household-notice" style={styles.toast}>
        <Text role="status" aria-live="polite" style={styles.message}>
          {notice.message}
        </Text>
        <Pressable role="button" aria-label="Dismiss" onPress={dismissNotice} hitSlop={8} style={styles.dismiss}>
          <Feather name="x" size={18} color={colors.text} aria-hidden accessible={false} />
        </Pressable>
      </View>
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    layer: {
      position: 'absolute' as const,
      left: 0,
      right: 0,
      alignItems: 'center' as const,
      paddingHorizontal: theme.spacing.lg,
    },
    toast: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.sm,
      maxWidth: 480,
      width: '100%' as const,
      paddingLeft: theme.spacing.lg,
      backgroundColor: theme.colors.primarySubtle,
      borderWidth: 1,
      borderColor: theme.colors.border,
      borderRadius: theme.radii.card,
    },
    message: { ...theme.typography.body, flex: 1, color: theme.colors.text, paddingVertical: theme.spacing.md },
    dismiss: {
      minWidth: MIN_TOUCH_TARGET,
      minHeight: MIN_TOUCH_TARGET,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
    },
  };
}

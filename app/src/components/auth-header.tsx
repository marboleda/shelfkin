import { Text, View } from 'react-native';

import { useThemedStyles, type Theme } from '@/theme';

import { APP_NAME } from './screen';

/** The brand name and tagline at the top of /sign-in and /sign-up. */
export function AuthHeader() {
  const styles = useThemedStyles(createStyles);
  return (
    <View style={styles.header}>
      <Text style={styles.brand}>{APP_NAME}</Text>
      <Text style={styles.tagline}>
        Know what&apos;s in the pantry, fridge and freezer, together with everyone you cook with.
      </Text>
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    header: { gap: theme.spacing.sm, paddingTop: theme.spacing.lg },
    brand: { ...theme.typography.heading, fontSize: 36, lineHeight: 42, color: theme.colors.primary },
    tagline: { ...theme.typography.body, fontSize: 18, lineHeight: 26, color: theme.colors.mutedText },
  };
}

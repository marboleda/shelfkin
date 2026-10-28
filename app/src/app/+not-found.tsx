import { Text } from 'react-native';

import { PressableLink } from '@/components/pressable-link';
import { Screen } from '@/components/screen';
import { ThemedText } from '@/components/themed-text';
import { useThemedStyles, type Theme } from '@/theme';

export default function NotFoundScreen() {
  const styles = useThemedStyles(createStyles);

  return (
    <Screen title="Page not found">
      <ThemedText variant="muted">We couldn&apos;t find the page you were looking for.</ThemedText>
      <PressableLink href="/" accessibilityRole="link" style={styles.link}>
        <Text style={styles.linkLabel}>Go to Pantry</Text>
      </PressableLink>
    </Screen>
  );
}

function createStyles(theme: Theme) {
  return {
    link: {
      alignSelf: 'flex-start' as const,
    },
    linkLabel: {
      ...theme.typography.label,
      color: theme.colors.primary,
    },
  };
}

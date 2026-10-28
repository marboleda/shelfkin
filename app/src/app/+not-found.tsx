import { Link } from 'expo-router';
import { Pressable, Text } from 'react-native';

import { Screen } from '@/components/screen';
import { ThemedText } from '@/components/themed-text';
import { useThemedStyles, type Theme } from '@/theme';

export default function NotFoundScreen() {
  const styles = useThemedStyles(createStyles);

  return (
    <Screen title="Page not found">
      <ThemedText variant="muted">We couldn&apos;t find the page you were looking for.</ThemedText>
      <Link href="/" asChild>
        <Pressable accessibilityRole="link" style={({ pressed }) => [styles.link, pressed && styles.pressed]}>
          <Text style={styles.linkLabel}>Go to Pantry</Text>
        </Pressable>
      </Link>
    </Screen>
  );
}

function createStyles(theme: Theme) {
  return {
    link: {
      alignSelf: 'flex-start' as const,
    },
    pressed: {
      opacity: 0.7,
    },
    linkLabel: {
      ...theme.typography.label,
      color: theme.colors.primary,
    },
  };
}

import type { ReactNode } from 'react';
import { Text, View } from 'react-native';

import { useThemedStyles, type Theme } from '@/theme';

type FormMessageProps = {
  tone: 'error' | 'info';
  message: string;
  /** Extra content after the message, such as a link. */
  children?: ReactNode;
  testID?: string;
};

/** A message about the whole form (not one field), announced when it appears. */
export function FormMessage({ tone, message, children, testID }: FormMessageProps) {
  const styles = useThemedStyles(createStyles);
  return (
    <View testID={testID} style={[styles.box, tone === 'error' ? styles.error : styles.info]}>
      <Text role={tone === 'error' ? 'alert' : 'status'} style={tone === 'error' ? styles.errorText : styles.infoText}>
        {message}
      </Text>
      {children}
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    box: {
      gap: theme.spacing.xs,
      padding: theme.spacing.md,
      borderRadius: theme.radii.md,
    },
    error: { backgroundColor: theme.colors.dangerSubtle },
    info: { backgroundColor: theme.colors.primarySubtle },
    errorText: { ...theme.typography.body, color: theme.colors.danger },
    infoText: { ...theme.typography.body, color: theme.colors.text },
  };
}

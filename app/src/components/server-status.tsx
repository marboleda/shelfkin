import { useFocusEffect } from 'expo-router';
import { useCallback, useRef } from 'react';
import { ActivityIndicator, Text, View } from 'react-native';

import { isApiError, userMessage } from '@/api/errors';
import { useHealthQuery } from '@/api/health';
import { useTheme, useThemedStyles, type Theme } from '@/theme';

export const SERVER_STATUS_TEXT = {
  checking: 'Checking…',
  connected: 'Connected',
  unreachable: "Can't reach the server",
} as const;

/**
 * The "Server" line on Settings: whether the API health endpoint answers.
 * Checks again each time the screen comes back into focus, so reopening
 * Settings after the API restarts shows the current state.
 */
export function ServerStatus() {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const { data, error, isFetching, refetch } = useHealthQuery();

  // The tab navigator keeps Settings mounted, so re-check whenever it is focused
  // again (the first focus is covered by the query's own initial fetch).
  const focusedBefore = useRef(false);
  useFocusEffect(
    useCallback(() => {
      if (focusedBefore.current) {
        refetch({ cancelRefetch: false });
      }
      focusedBefore.current = true;
    }, [refetch]),
  );

  let status: string;
  let statusStyle = styles.value;
  if (isFetching) {
    status = SERVER_STATUS_TEXT.checking;
  } else if (data) {
    status = SERVER_STATUS_TEXT.connected;
    statusStyle = styles.connected;
  } else if (isApiError(error) && error.kind === 'network') {
    status = SERVER_STATUS_TEXT.unreachable;
    statusStyle = styles.problem;
  } else {
    // The API answered, but with an error, e.g. 503 when its database is down.
    status = userMessage(error);
    statusStyle = styles.problem;
  }

  return (
    <View testID="server-status" style={styles.row} aria-busy={isFetching}>
      <Text style={styles.label}>Server</Text>
      <View style={styles.valueRow}>
        {isFetching ? <ActivityIndicator size="small" color={theme.colors.mutedText} /> : null}
        <Text testID="server-status-text" role="status" style={statusStyle}>
          {status}
        </Text>
      </View>
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    row: {
      flexDirection: 'row' as const,
      justifyContent: 'space-between' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.md,
      paddingVertical: theme.spacing.md,
      paddingHorizontal: theme.spacing.lg,
      backgroundColor: theme.colors.surface,
      borderColor: theme.colors.border,
      borderWidth: 1,
      borderRadius: theme.radii.md,
    },
    label: { ...theme.typography.label, color: theme.colors.text },
    valueRow: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.sm,
      flexShrink: 1,
    },
    value: { ...theme.typography.body, color: theme.colors.mutedText },
    connected: { ...theme.typography.body, color: theme.colors.primary },
    problem: { ...theme.typography.body, color: theme.colors.danger, flexShrink: 1 },
  };
}

import { ActivityIndicator, Text, View } from 'react-native';

import { userMessage } from '@/api/errors';
import { useMeQuery } from '@/auth/profile';
import { AddItemButton } from '@/components/add-item-button';
import { Button } from '@/components/button';
import { HouseholdSwitcher } from '@/components/household-switcher';
import { PantryList } from '@/components/pantry-list';
import { Screen } from '@/components/screen';
import { ThemedText } from '@/components/themed-text';
import { useIsWideLayout } from '@/hooks/use-is-wide-layout';
import { useNow } from '@/hooks/use-now';
import { useHouseholdKeys } from '@/household/current-household';
import { useLocationNames, usePantryQuery } from '@/pantry/pantry';
import { useTheme, useThemedStyles, type Theme } from '@/theme';

/** Room below the last row so the floating Add item button never covers it. */
const FLOATING_BUTTON_CLEARANCE = 96;

export default function PantryScreen() {
  const wide = useIsWideLayout();
  const { householdId } = useHouseholdKeys();

  // Below the breakpoint the header is the household switcher; at and above it
  // the switcher is in the side nav and the header is a plain "Pantry".
  return (
    <Screen
      title="Pantry"
      inShell
      scroll={false}
      header={wide ? undefined : <HouseholdSwitcher placement="header" />}
    >
      {/* Keyed by household, so expanded rows reset when the household changes. */}
      <PantryContent key={householdId} wide={wide} />
    </Screen>
  );
}

function PantryContent({ wide }: { wide: boolean }) {
  const styles = useThemedStyles(createStyles);
  const pantry = usePantryQuery();
  const locationName = useLocationNames();
  const me = useMeQuery();
  const now = useNow();

  const addButton = <AddItemButton placement={wide ? 'inline' : 'floating'} />;

  let body;
  if (pantry.data) {
    const refreshError = pantry.isError ? (
      <View role="alert" testID="pantry-refresh-error" style={styles.refreshError}>
        <ThemedText style={styles.refreshErrorText}>
          Couldn&apos;t refresh the pantry. {userMessage(pantry.error)}
        </ThemedText>
        <Button
          variant="link"
          label="Retry"
          busy={pantry.isFetching}
          onPress={() => void pantry.refetch()}
        />
      </View>
    ) : null;

    body =
      pantry.data.groups.length === 0 ? (
        <View style={styles.fill}>
          {refreshError}
          <View testID="pantry-empty" style={styles.centered}>
            <Text role="heading" aria-level={2} style={styles.emptyTitle}>
              Your pantry is empty
            </Text>
            <ThemedText variant="muted" style={styles.centeredText}>
              Add what you have on hand to keep track of it.
            </ThemedText>
            <AddItemButton label="Add your first item" testID="pantry-empty-add" />
          </View>
        </View>
      ) : (
        <PantryList
          pantry={pantry.data}
          now={now}
          currentUserId={me.data?.id ?? null}
          locationName={locationName}
          header={refreshError ? <View style={styles.listHeader}>{refreshError}</View> : null}
          bottomInset={wide ? 24 : FLOATING_BUTTON_CLEARANCE}
        />
      );
  } else if (pantry.isError) {
    body = (
      <View testID="pantry-error" role="alert" style={styles.centered}>
        <ThemedText style={styles.centeredText}>{userMessage(pantry.error)}</ThemedText>
        <Button
          variant="secondary"
          label="Retry"
          busy={pantry.isFetching}
          onPress={() => void pantry.refetch()}
        />
      </View>
    );
  } else {
    body = <PantryLoading />;
  }

  return (
    <View style={styles.fill}>
      {wide ? addButton : null}
      {body}
      {wide ? null : addButton}
    </View>
  );
}

/** Placeholder rows while the pantry first loads (never the empty state). */
function PantryLoading() {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  return (
    <View testID="pantry-loading" aria-busy style={styles.loading}>
      <ActivityIndicator color={theme.colors.primary} accessibilityLabel="Loading pantry" />
      {[0, 1, 2, 3].map((index) => (
        <View key={index} style={styles.placeholderRow} aria-hidden>
          <View style={[styles.placeholderBar, styles.placeholderName]} />
          <View style={[styles.placeholderBar, styles.placeholderDetail]} />
        </View>
      ))}
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    fill: {
      flex: 1,
      gap: theme.spacing.lg,
    },
    listHeader: {
      paddingBottom: theme.spacing.sm,
    },
    refreshError: {
      flexDirection: 'row' as const,
      flexWrap: 'wrap' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.sm,
      padding: theme.spacing.md,
      borderRadius: theme.radii.md,
      borderWidth: 1,
      borderColor: theme.colors.danger,
      backgroundColor: theme.colors.dangerSubtle,
    },
    refreshErrorText: {
      ...theme.typography.caption,
      flex: 1,
      minWidth: 200,
      color: theme.colors.text,
    },
    centered: {
      flex: 1,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      gap: theme.spacing.lg,
      paddingVertical: theme.spacing.xxl,
    },
    centeredText: {
      textAlign: 'center' as const,
    },
    emptyTitle: {
      ...theme.typography.title,
      color: theme.colors.text,
    },
    loading: {
      gap: theme.spacing.sm,
    },
    placeholderRow: {
      gap: theme.spacing.sm,
      padding: theme.spacing.lg,
      borderRadius: theme.radii.card,
      borderWidth: 1,
      borderColor: theme.colors.border,
      backgroundColor: theme.colors.surface,
    },
    placeholderBar: {
      height: 12,
      borderRadius: theme.radii.sm,
      backgroundColor: theme.colors.levelEmpty,
    },
    placeholderName: { width: '45%' as const },
    placeholderDetail: { width: '70%' as const },
  };
}

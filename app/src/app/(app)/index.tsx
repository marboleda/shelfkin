import { useEffect, useMemo, useState } from 'react';
import { ActivityIndicator, Text, View } from 'react-native';

import { userMessage } from '@/api/errors';
import { useMeQuery } from '@/auth/profile';
import { AddItemButton } from '@/components/add-item-button';
import { Button } from '@/components/button';
import { HouseholdSwitcher } from '@/components/household-switcher';
import { ExpiringCard, FilterLine, FilterPanel, PantryToolbar } from '@/components/pantry-controls';
import { PantryList } from '@/components/pantry-list';
import { Screen } from '@/components/screen';
import { ThemedText } from '@/components/themed-text';
import { useIsWideLayout } from '@/hooks/use-is-wide-layout';
import { useNow } from '@/hooks/use-now';
import { useHouseholdKeys } from '@/household/current-household';
import { useCategoriesQuery, useLocationNames, useLocationsQuery, usePantryQuery } from '@/pantry/pantry';
import {
  buildSections,
  expiringSummary,
  hasNoEntries,
  knownFilters,
  NO_FILTERS,
  type PantryFilters,
  type ViewMode,
} from '@/pantry/sections';
import { usePantryView, type PantryView } from '@/pantry/use-pantry-view';
import { useTheme, useThemedStyles, type Theme } from '@/theme';

/** Room below the last row so the floating Add item button never covers it. */
const FLOATING_BUTTON_CLEARANCE = 96;

export default function PantryScreen() {
  const wide = useIsWideLayout();
  const { householdId } = useHouseholdKeys();
  // Read here, outside the household-keyed content, so switching household
  // keeps the view and filters without reading them again.
  const view = usePantryView();

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
      <PantryContent key={householdId} wide={wide} view={view} />
    </Screen>
  );
}

function PantryContent({ wide, view }: { wide: boolean; view: PantryView }) {
  const styles = useThemedStyles(createStyles);
  const pantry = usePantryQuery();
  const locations = useLocationsQuery();
  const categories = useCategoriesQuery();
  const locationName = useLocationNames();
  const me = useMeQuery();
  const now = useNow();
  const [panelOpen, setPanelOpen] = useState(false);

  const mode: ViewMode = view.choices?.mode ?? 'location';
  // From the API's expiryStatus only, across the whole pantry (filters aside).
  const expiring = useMemo(() => expiringSummary(pantry.data?.groups ?? []), [pantry.data]);
  // Saved IDs that are not in the loaded lists are ignored, and dropped the
  // next time the choices are saved (every change saves these). Expiring is
  // ignored while nothing is expiring, and saved back as off below.
  const filters = useMemo<PantryFilters>(
    () =>
      view.choices && locations.data && categories.data
        ? knownFilters(view.choices.filters, locations.data, categories.data, expiring.count)
        : NO_FILTERS,
    [view.choices, locations.data, categories.data, expiring.count],
  );
  // A saved Expiring with nothing expiring is saved back as off, so the chip
  // does not come back already on when something starts expiring later. Only
  // once the pantry has loaded successfully: a slow or failed request (the
  // query's status is 'error' after a failed refresh, even with data) never
  // wipes the choice. The rest of the saved choices are kept as saved.
  const savedChoices = view.choices;
  const setChoices = view.setChoices;
  const staleExpiring = pantry.isSuccess && savedChoices?.filters.expiring === true && expiring.count === 0;
  useEffect(() => {
    if (staleExpiring && savedChoices) {
      setChoices({ ...savedChoices, filters: { ...savedChoices.filters, expiring: false } });
    }
  }, [staleExpiring, savedChoices, setChoices]);
  // Worked out from what is already loaded, so toggling and filtering send no request.
  const sections = useMemo(
    () =>
      pantry.data && locations.data && categories.data
        ? buildSections(pantry.data.groups, { mode, filters, locations: locations.data, categories: categories.data })
        : [],
    [pantry.data, locations.data, categories.data, mode, filters],
  );

  const addButton = <AddItemButton placement={wide ? 'inline' : 'floating'} />;
  // Locations and categories never refetch once loaded, so only their first load can fail.
  const failed = [pantry, locations, categories].filter((query) => query.isError && !query.data);

  let body;
  if (pantry.data && locations.data && categories.data && view.loaded) {
    const setMode = (next: ViewMode) => view.setChoices({ mode: next, filters });
    const setFilters = (next: PantryFilters) => view.setChoices({ mode, filters: next });
    const clearFilters = () => setFilters(NO_FILTERS);
    const setExpiring = (on: boolean) => setFilters({ ...filters, expiring: on });

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

    body = hasNoEntries(pantry.data.groups) ? (
      // The remembered view and filters are kept for when there are entries again.
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
      // The controls are the list's header, so they scroll with the rows: an
      // open filter panel never pushes the rows (or its own chips) off screen,
      // and the bottom inset keeps the last chip and row clear of the floating
      // Add item button.
      <PantryList
        sections={sections}
        mode={mode}
        today={pantry.data.today}
        now={now}
        currentUserId={me.data?.id ?? null}
        locationName={locationName}
        header={
          <View style={styles.controls}>
            {refreshError}
            <PantryToolbar
              mode={mode}
              onModeChange={setMode}
              filters={filters}
              panelOpen={panelOpen}
              onTogglePanel={() => setPanelOpen((open) => !open)}
              expiringCount={expiring.count}
              onToggleExpiring={() => setExpiring(!filters.expiring)}
              wide={wide}
            />
            {panelOpen ? (
              <FilterPanel
                locations={locations.data}
                categories={categories.data}
                filters={filters}
                onChange={setFilters}
              />
            ) : null}
            <FilterLine
              locations={locations.data}
              categories={categories.data}
              filters={filters}
              onClear={clearFilters}
            />
            {expiring.count > 0 && !filters.expiring ? (
              <ExpiringCard count={expiring.count} names={expiring.names} onPress={() => setExpiring(true)} />
            ) : null}
          </View>
        }
        empty={
          <View testID="pantry-no-matches" style={styles.centered}>
            <Text role="heading" aria-level={2} style={styles.emptyTitle}>
              No items match these filters
            </Text>
            <Button variant="secondary" label="Clear filters" onPress={clearFilters} />
          </View>
        }
        bottomInset={wide ? 24 : FLOATING_BUTTON_CLEARANCE}
      />
    );
  } else if (failed.length > 0) {
    body = (
      <View testID="pantry-error" role="alert" style={styles.centered}>
        <ThemedText style={styles.centeredText}>{userMessage(failed[0].error)}</ThemedText>
        <Button
          variant="secondary"
          label="Retry"
          busy={failed.some((query) => query.isFetching)}
          onPress={() => {
            for (const query of failed) void query.refetch();
          }}
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
    controls: {
      gap: theme.spacing.md,
      paddingBottom: theme.spacing.lg,
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

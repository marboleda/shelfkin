// Entry detail: one pantry entry, with its level, location and dates to
// change and a way to delete it. Every change is saved at once (one request
// per change, see src/pantry/entry-mutations.ts); there is no Save button.

import { useQuery } from '@tanstack/react-query';
import { useFocusEffect, useRouter } from 'expo-router';
import { useCallback, useState } from 'react';
import { ActivityIndicator, Text, View } from 'react-native';

import { userMessage } from '@/api/errors';
import { useMeQuery } from '@/auth/profile';
import { useNow } from '@/hooks/use-now';
import { useCurrentHousehold, useHouseholdKeys } from '@/household/current-household';
import { findEntry } from '@/pantry/entry-cache';
import { useDeleteEntry, useUpdateEntry, type ChangeOptions, type EntryChange } from '@/pantry/entry-mutations';
import { expiryLabel, formatCalendarDate, lastChangeLabel, type StockLevel } from '@/pantry/format';
import { fetchPantry, useLocationNames, useLocationsQuery, type PantryGroup, type PantryListEntry } from '@/pantry/pantry';
import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

import { BackToPantry } from './back-to-pantry';
import { Button } from './button';
import { ChoiceChips } from './choice-chips';
import { ConfirmDialog } from './confirm-dialog';
import { DateField } from './date-field';
import { ExpiryChip } from './expiry-chip';
import { FormMessage } from './form-message';
import { LevelPicker } from './level-picker';
import { PressableLink } from './pressable-link';
import { Screen } from './screen';
import { DATE_SAVE_MODE, useDateDraft, type DateSaveOptions } from './use-date-draft';

/**
 * The detail screen for `/entries/<id>`. It reads the entry from the current
 * household's cached pantry, and only loads the pantry when it is not cached
 * yet (a direct visit or a reload on web).
 */
export function EntryDetail({ entryId }: { entryId: string }) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const keys = useHouseholdKeys();
  const household = useCurrentHousehold();
  const pantry = useQuery({
    queryKey: keys.pantry(),
    queryFn: ({ signal }) => fetchPantry(keys.householdId, signal),
    // Use the cached pantry as it is: opening an entry sends no request.
    refetchOnMount: false,
  });

  const found = findEntry(pantry.data, entryId);

  if (found && pantry.data) {
    return (
      <Screen title={found.group.name} inShell accessory={<BackToPantry />}>
        <EntryForm key={found.entry.id} group={found.group} entry={found.entry} today={pantry.data.today} />
      </Screen>
    );
  }

  let body;
  if (pantry.data) {
    body = (
      <View testID="entry-missing" style={styles.centered}>
        <Text role="alert" style={styles.missingText}>
          This item isn&apos;t in {household.name} any more
        </Text>
        <PressableLink href="/" accessibilityRole="link" style={styles.linkButton} testID="entry-missing-back">
          <Text style={styles.linkLabel}>Back to Pantry</Text>
        </PressableLink>
      </View>
    );
  } else if (pantry.isError) {
    body = (
      <View testID="entry-error" role="alert" style={styles.centered}>
        <Text style={styles.missingText}>{userMessage(pantry.error)}</Text>
        <Button variant="secondary" label="Retry" busy={pantry.isFetching} onPress={() => void pantry.refetch()} />
      </View>
    );
  } else {
    body = (
      <View testID="entry-loading" aria-busy style={styles.centered}>
        <ActivityIndicator color={theme.colors.primary} accessibilityLabel="Loading" />
      </View>
    );
  }

  return (
    <Screen title="Entry detail" inShell accessory={<BackToPantry />}>
      {body}
    </Screen>
  );
}

type EntryFormProps = {
  group: PantryGroup;
  entry: PantryListEntry;
  today: string;
};

function EntryForm({ group, entry, today }: EntryFormProps) {
  const styles = useThemedStyles(createStyles);
  const router = useRouter();
  const me = useMeQuery();
  const now = useNow();
  const locations = useLocationsQuery();
  const locationName = useLocationNames();
  const { change } = useUpdateEntry(entry.id);
  const { remove } = useDeleteEntry(entry.id);
  const [confirming, setConfirming] = useState(false);

  const name = group.name;
  const bought = formatCalendarDate(entry.purchasedOn, today);
  const location = locationName(entry.locationId);
  const expiry = expiryLabel(entry, today);
  const lastChange = lastChangeLabel(entry.lastChange, {
    currentUserId: me.data?.id ?? null,
    locationName,
    now,
  });

  /** Sends `next` unless the entry already has that value. */
  const send = (next: EntryChange, options?: ChangeOptions) => {
    if (entry[next.field] === next.value) return;
    change(next, name, options);
  };

  const confirmDelete = () => {
    setConfirming(false);
    remove(name);
    router.navigate('/');
  };

  return (
    <View style={styles.form} testID="entry-detail">
      <View style={styles.summary}>
        <View style={styles.summaryLine}>
          <Text testID="entry-bought" style={styles.summaryText}>
            {location ? `Bought ${bought} · ${location}` : `Bought ${bought}`}
          </Text>
          <ExpiryChip label={expiry} />
        </View>
        {lastChange ? (
          <Text testID="last-change" style={styles.caption}>
            {lastChange}
          </Text>
        ) : null}
      </View>

      <LevelPicker
        label="Level"
        testID="entry-level"
        value={entry.level}
        onChange={(level: StockLevel) => send({ field: 'level', value: level })}
      />

      {locations.data ? (
        <ChoiceChips
          label="Location"
          testID="entry-location"
          options={locations.data.map((option) => ({ value: option.id, label: option.name }))}
          value={entry.locationId}
          onChange={(locationId) => send({ field: 'locationId', value: locationId })}
        />
      ) : locations.isError ? (
        <FormMessage tone="error" message="Couldn't load locations" testID="entry-locations-error">
          <Button variant="link" label="Retry" onPress={() => void locations.refetch()} />
        </FormMessage>
      ) : null}

      <EntryDateField
        label="Bought"
        testID="entry-bought-date"
        value={entry.purchasedOn}
        onSave={(value, options) => send({ field: 'purchasedOn', value }, options)}
        incompleteHint="Enter the date you bought it"
      />

      <View style={styles.expiry}>
        <EntryDateField
          label="Expires"
          testID="entry-expires"
          value={entry.expiresOn ?? ''}
          onSave={(value, options) => send({ field: 'expiresOn', value }, options)}
          incompleteHint={entry.expiresOn ? 'Enter a date, or choose No expiry' : null}
          hint={entry.expiresOn ? null : 'No expiry'}
        />
        {entry.expiresOn ? (
          <View style={styles.noExpiry}>
            <Button
              variant="link"
              label="No expiry"
              accessibilityHint="Removes the expiry date"
              onPress={() => send({ field: 'expiresOn', value: null })}
              testID="entry-no-expiry"
            />
          </View>
        ) : null}
      </View>

      <View style={styles.itemSection}>
        <Text role="heading" aria-level={2} style={styles.sectionHeading}>
          For all {name}
        </Text>
        <PressableLink
          href={{ pathname: '/items/[id]', params: { id: group.catalogItemId, from: entry.id } }}
          accessibilityRole="link"
          accessibilityHint={`Default category and location for ${name}`}
          style={styles.linkRow}
          testID="entry-item-settings"
        >
          <Text style={styles.linkLabel}>Item settings ›</Text>
        </PressableLink>
      </View>

      <Button variant="danger" label="Delete this entry" onPress={() => setConfirming(true)} testID="entry-delete" />

      <ConfirmDialog
        visible={confirming}
        title={`Delete ${name} (${bought})?`}
        message="This can't be undone."
        confirmLabel="Delete"
        destructive
        onConfirm={confirmDelete}
        onCancel={() => setConfirming(false)}
        testID="entry-delete-dialog"
      />
    </View>
  );
}

type EntryDateFieldProps = {
  label: string;
  testID: string;
  /** The saved date, `YYYY-MM-DD`, or '' for none. */
  value: string;
  /** Called with a new valid date (see `DateSaveOptions`). */
  onSave: (value: string, options?: DateSaveOptions) => void;
  /** Shown while the field holds something that is not a whole date (web only). */
  incompleteHint: string | null;
  hint?: string | null;
};

/**
 * A `DateField` that saves the dates it is given. A native picker's date is
 * saved at once. On web a typed date is saved when the person has finished
 * (leaving the field, Enter, leaving the screen or hiding the page), never
 * after a pause, since `<input type="date">` reports a valid date after every
 * digit of the year. Until then what is typed is kept, so a half-typed date is
 * neither saved nor replaced by the saved one (see use-date-draft.ts).
 */
function EntryDateField({ label, testID, value, onSave, incompleteHint, hint }: EntryDateFieldProps) {
  const draft = useDateDraft(value, onSave, DATE_SAVE_MODE);
  const { commit } = draft;
  // Leaving the screen doesn't always unmount it (the app's screens are tabs), so save on blur too.
  useFocusEffect(useCallback(() => commit, [commit]));
  return (
    <DateField
      label={label}
      testID={testID}
      value={draft.shown}
      onChange={draft.onChange}
      onBlur={commit}
      onSubmitEditing={commit}
      reserveHintSpace
      hint={draft.status === 'incomplete' ? incompleteHint : draft.status === 'unsaved' ? UNSAVED_DATE_HINT : hint}
    />
  );
}

/** The hint under a typed date that is not saved yet (web). */
const UNSAVED_DATE_HINT = 'Press Enter or leave the field to save';

function createStyles(theme: Theme) {
  return {
    form: { gap: theme.spacing.xl },
    summary: { gap: theme.spacing.xs + 2 },
    summaryLine: {
      flexDirection: 'row' as const,
      flexWrap: 'wrap' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.sm,
    },
    summaryText: { ...theme.typography.body, color: theme.colors.mutedText },
    caption: { ...theme.typography.caption, color: theme.colors.mutedText },
    expiry: { gap: theme.spacing.xs },
    noExpiry: { alignSelf: 'flex-start' as const },
    centered: {
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      gap: theme.spacing.lg,
      paddingVertical: theme.spacing.xxl,
    },
    missingText: { ...theme.typography.body, color: theme.colors.text, textAlign: 'center' as const },
    linkButton: {
      minHeight: MIN_TOUCH_TARGET,
      justifyContent: 'center' as const,
      paddingHorizontal: theme.spacing.md,
    },
    linkLabel: { ...theme.typography.label, color: theme.colors.primary },
    itemSection: { gap: theme.spacing.xs },
    sectionHeading: { ...theme.typography.label, color: theme.colors.mutedText },
    linkRow: { minHeight: MIN_TOUCH_TARGET, justifyContent: 'center' as const, alignSelf: 'flex-start' as const },
  };
}

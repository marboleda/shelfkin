// Item settings: the current household's defaults for one catalog item, its
// category, location and (custom items only) shelf life. Each field is saved
// on its own as soon as it changes (see src/catalog/catalog-item.ts).

import { useState } from 'react';
import { ActivityIndicator, Text, View } from 'react-native';

import { isApiError, userMessage } from '@/api/errors';
import { MAX_SHELF_LIFE_DAYS, MIN_SHELF_LIFE_DAYS, type CatalogItem } from '@/catalog/catalog';
import {
  parseShelfLifeDays,
  useCatalogItemQuery,
  useUpdateCatalogItem,
  type DefaultsChange,
} from '@/catalog/catalog-item';
import { useCurrentHousehold } from '@/household/current-household';
import { useCategoriesQuery, useLocationNames, useLocationsQuery } from '@/pantry/pantry';
import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

import { BackToPantry } from './back-to-pantry';
import { Button } from './button';
import { ChoiceChips } from './choice-chips';
import { FormMessage } from './form-message';
import { PressableLink } from './pressable-link';
import { Screen } from './screen';
import { TextField } from './text-field';

export const SHELF_LIFE_ERROR = `Enter a whole number of days from ${MIN_SHELF_LIFE_DAYS} to ${MAX_SHELF_LIFE_DAYS}, or leave it empty`;

type ItemSettingsProps = {
  itemId: string;
  /** The entry the screen was opened from, for the back link. Pantry when missing. */
  fromEntryId?: string | null;
};

/** The screen for `/items/<catalogItemId>`. */
export function ItemSettings({ itemId, fromEntryId }: ItemSettingsProps) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const item = useCatalogItemQuery(itemId);
  const back = <BackLink fromEntryId={fromEntryId} />;

  if (item.data) {
    return (
      <Screen title={item.data.name} inShell accessory={back}>
        <ItemSettingsForm item={item.data} />
      </Screen>
    );
  }

  let body;
  if (item.isError && isApiError(item.error) && item.error.status === 404) {
    body = (
      <View testID="item-missing" style={styles.centered}>
        <Text role="alert" style={styles.centeredText}>
          This item isn&apos;t available
        </Text>
        <PressableLink
          href={fromEntryId ? { pathname: '/entries/[id]', params: { id: fromEntryId } } : '/'}
          accessibilityRole="link"
          style={styles.linkButton}
          testID="item-missing-back"
        >
          <Text style={styles.linkLabel}>{fromEntryId ? 'Back to the entry' : 'Back to Pantry'}</Text>
        </PressableLink>
      </View>
    );
  } else if (item.isError) {
    body = (
      <View testID="item-error" role="alert" style={styles.centered}>
        <Text style={styles.centeredText}>{userMessage(item.error)}</Text>
        <Button variant="secondary" label="Retry" busy={item.isFetching} onPress={() => void item.refetch()} />
      </View>
    );
  } else {
    body = (
      <View testID="item-loading" aria-busy style={styles.centered}>
        <ActivityIndicator color={theme.colors.primary} accessibilityLabel="Loading" />
      </View>
    );
  }

  return (
    <Screen title="Item settings" inShell accessory={back}>
      {body}
    </Screen>
  );
}

function BackLink({ fromEntryId }: { fromEntryId?: string | null }) {
  const styles = useThemedStyles(createStyles);
  if (!fromEntryId) return <BackToPantry />;
  return (
    <PressableLink
      href={{ pathname: '/entries/[id]', params: { id: fromEntryId } }}
      accessibilityRole="link"
      accessibilityLabel="Back to the entry"
      hitSlop={8}
      style={styles.back}
      testID="item-back"
    >
      <Text style={styles.linkLabel}>‹ Back</Text>
    </PressableLink>
  );
}

function ItemSettingsForm({ item }: { item: CatalogItem }) {
  const styles = useThemedStyles(createStyles);
  const household = useCurrentHousehold();
  const categories = useCategoriesQuery();
  const locations = useLocationsQuery();
  const locationName = useLocationNames();
  const { change, pending } = useUpdateCatalogItem(item.id);
  // The location before the first change made here, to say where new entries go once it differs.
  const [locationBefore, setLocationBefore] = useState<number | null>(null);

  const send = (next: DefaultsChange) => {
    if (next.value !== null && item[next.field] === next.value) return;
    if (next.field === 'locationId' && locationBefore === null) setLocationBefore(item.locationId);
    change(next, item.name);
  };

  const newLocation = locationName(item.locationId);
  const showLocationNote = locationBefore !== null && locationBefore !== item.locationId && !!newLocation;

  return (
    <View style={styles.form} testID="item-settings">
      <Text testID="item-kind" style={styles.kind}>
        {item.isCustom ? 'Your household’s item' : 'Shelfkin item'}
      </Text>

      <View style={styles.field}>
        {categories.data ? (
          <ChoiceChips
            label="Default category"
            testID="item-category"
            options={categories.data.map((option) => ({ value: option.id, label: option.name }))}
            value={item.categoryId}
            onChange={(categoryId) => send({ field: 'categoryId', value: categoryId })}
          />
        ) : (
          <ListState
            label="Default category"
            query={categories}
            error="Couldn't load categories"
            testID="item-categories"
          />
        )}
        <FieldStatus
          item={item}
          overridden={item.categoryOverridden}
          saving={pending.includes('categoryId')}
          onReset={() => send({ field: 'categoryId', value: null })}
          testID="item-category"
        />
      </View>

      <View style={styles.field}>
        {locations.data ? (
          <ChoiceChips
            label="Default location"
            testID="item-location"
            options={locations.data.map((option) => ({ value: option.id, label: option.name }))}
            value={item.locationId}
            onChange={(locationId) => send({ field: 'locationId', value: locationId })}
          />
        ) : (
          <ListState label="Default location" query={locations} error="Couldn't load locations" testID="item-locations" />
        )}
        <FieldStatus
          item={item}
          overridden={item.locationOverridden}
          saving={pending.includes('locationId')}
          onReset={() => send({ field: 'locationId', value: null })}
          testID="item-location"
        />
        {showLocationNote ? (
          <Text role="status" testID="item-location-note" style={styles.note}>
            New {item.name} entries will go to {newLocation}. Existing entries stay where they are.
          </Text>
        ) : null}
      </View>

      {item.isCustom ? (
        <ShelfLifeField
          value={item.shelfLifeDays}
          saving={pending.includes('shelfLifeDays')}
          onSave={(days) => send({ field: 'shelfLifeDays', value: days })}
        />
      ) : null}

      <Text testID="item-scope" style={styles.caption}>
        Saved for {household.name} and used the next time anyone adds {item.name}.
      </Text>
    </View>
  );
}

type ListQuery = { isError: boolean; refetch: () => unknown; isFetching: boolean };

/** Where the category or location chips go while their list loads or after it fails. */
function ListState({ label, query, error, testID }: { label: string; query: ListQuery; error: string; testID: string }) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  return (
    <View style={styles.field}>
      <Text style={styles.label}>{label}</Text>
      {query.isError ? (
        <FormMessage tone="error" message={error} testID={`${testID}-error`}>
          <Button variant="link" label="Retry" busy={query.isFetching} onPress={() => void query.refetch()} />
        </FormMessage>
      ) : (
        <ActivityIndicator color={theme.colors.primary} accessibilityLabel="Loading" style={styles.start} />
      )}
    </View>
  );
}

type FieldStatusProps = {
  item: CatalogItem;
  overridden: boolean;
  saving: boolean;
  onReset: () => void;
  testID: string;
};

/**
 * Under a category or location: for a built-in item, whether the value is the
 * household's own ("Household setting", with a reset) or Shelfkin's; and a
 * spinner while a change is saved.
 */
function FieldStatus({ item, overridden, saving, onReset, testID }: FieldStatusProps) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  if (item.isCustom && !saving) return null;
  return (
    <View style={styles.status}>
      {item.isCustom ? null : (
        <Text testID={`${testID}-source`} style={styles.caption}>
          {overridden ? 'Household setting' : 'Shelfkin default'}
        </Text>
      )}
      {saving ? (
        <ActivityIndicator
          testID={`${testID}-saving`}
          size="small"
          color={theme.colors.primary}
          accessibilityLabel="Saving"
        />
      ) : null}
      {!item.isCustom && overridden ? (
        <Button variant="link" label="Reset to Shelfkin default" onPress={onReset} testID={`${testID}-reset`} />
      ) : null}
    </View>
  );
}

type ShelfLifeFieldProps = {
  /** The saved shelf life, or null for none. */
  value: number | null;
  saving: boolean;
  /** Called with a new valid shelf life, or null to clear it. */
  onSave: (days: number | null) => void;
};

/**
 * A custom item's shelf life. What is typed is kept until the field is left
 * or submitted; it is then saved when it is a whole number from 1 to 3650,
 * cleared when empty, and otherwise kept with a message.
 */
function ShelfLifeField({ value, saving, onSave }: ShelfLifeFieldProps) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const [draft, setDraft] = useState<{ text: string; saved: number | null } | null>(null);
  const savedText = value === null ? '' : String(value);
  // A draft is dropped once the saved value changes under it (a save, a rollback, someone else's change).
  const shown = draft && draft.saved === value ? draft.text : savedText;
  const invalid = shown.trim() !== '' && parseShelfLifeDays(shown) === null;
  const canClear = value !== null && !invalid;

  const commit = () => {
    if (invalid) return;
    setDraft(null);
    const days = shown.trim() === '' ? null : parseShelfLifeDays(shown);
    if (days !== value) onSave(days);
  };

  return (
    <View style={styles.field}>
      <TextField
        label="Usually keeps for … days"
        testID="item-shelf-life"
        value={shown}
        onChangeText={(text) => setDraft({ text, saved: value })}
        onBlur={commit}
        onSubmitEditing={commit}
        keyboardType="number-pad"
        inputMode="numeric"
        returnKeyType="done"
        maxLength={4}
        placeholder="No typical shelf life"
        error={invalid ? SHELF_LIFE_ERROR : null}
      />
      {saving || canClear ? (
        <View style={styles.status}>
          {saving ? (
            <ActivityIndicator
              testID="item-shelf-life-saving"
              size="small"
              color={theme.colors.primary}
              accessibilityLabel="Saving"
            />
          ) : null}
          {canClear ? (
            <Button
              variant="link"
              label="Clear shelf life"
              onPress={() => {
                setDraft(null);
                onSave(null);
              }}
              testID="item-shelf-life-clear"
            />
          ) : null}
        </View>
      ) : null}
      {value === null && !invalid ? (
        <Text style={styles.caption}>Optional. Used to suggest an expiry date when it is added.</Text>
      ) : null}
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    form: { gap: theme.spacing.xl },
    field: { gap: theme.spacing.sm },
    kind: { ...theme.typography.body, color: theme.colors.mutedText },
    label: { ...theme.typography.label, color: theme.colors.text },
    caption: { ...theme.typography.caption, color: theme.colors.mutedText },
    note: { ...theme.typography.caption, color: theme.colors.text },
    status: {
      flexDirection: 'row' as const,
      flexWrap: 'wrap' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.md,
      minHeight: MIN_TOUCH_TARGET,
    },
    start: { alignSelf: 'flex-start' as const },
    centered: {
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      gap: theme.spacing.lg,
      paddingVertical: theme.spacing.xxl,
    },
    centeredText: { ...theme.typography.body, color: theme.colors.text, textAlign: 'center' as const },
    back: {
      alignSelf: 'flex-start' as const,
      paddingVertical: theme.spacing.xs,
      paddingHorizontal: theme.spacing.sm,
      marginLeft: -theme.spacing.sm,
      borderRadius: theme.radii.sm,
    },
    linkButton: {
      minHeight: MIN_TOUCH_TARGET,
      justifyContent: 'center' as const,
      paddingHorizontal: theme.spacing.md,
    },
    linkLabel: { ...theme.typography.label, color: theme.colors.primary },
  };
}

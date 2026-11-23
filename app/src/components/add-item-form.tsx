import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { useEffect, useMemo, useRef, useState, type ComponentRef } from 'react';
import { ActivityIndicator, Pressable, ScrollView, Text, View } from 'react-native';

import { isApiError, userMessage } from '@/api/errors';
import { queryKeys } from '@/api/query-keys';
import {
  catalogQueriesKey,
  cleanItemName,
  createCatalogItem,
  MAX_SHELF_LIFE_DAYS,
  MIN_SHELF_LIFE_DAYS,
  sameItemName,
  searchCatalog,
  type CatalogItem,
} from '@/catalog/catalog';
import { useHouseholdKeys, useOptionalCurrentHousehold } from '@/household/current-household';
import { newId } from '@/lib/ids';
import { queryRetryDelay, withRetries } from '@/lib/retry';
import { addDays, isValidCalendarDate, todayOnDevice } from '@/pantry/dates';
import { createEntry, ID_CONFLICT_CODES, type PantryEntry } from '@/pantry/entries';
import type { StockLevel } from '@/pantry/format';
import { fetchPantry, useCategoriesQuery, useLocationsQuery } from '@/pantry/pantry';
import { useTheme, useThemedStyles, type Theme } from '@/theme';

import { AddItemSearch } from './add-item-search';
import { Button } from './button';
import { ChoiceChips } from './choice-chips';
import { DateField } from './date-field';
import { focusElement } from './focus';
import { FormMessage } from './form-message';
import { LevelPicker } from './level-picker';
import { TextField } from './text-field';

/** What the Details step is adding: an item from the catalog, or a new custom item. */
export type PickedItem = { kind: 'catalog'; item: CatalogItem } | { kind: 'new'; name: string };

type WriteIds = { id: string; eventId: string };

function newWriteIds(): WriteIds {
  return { id: newId(), eventId: newId() };
}

/** The expiry follows the shelf-life suggestion until the user sets or clears it by hand. */
type Expiry = { mode: 'suggested' } | { mode: 'manual'; value: string };

/** Thrown when creating a custom item finds the name taken; `existing` is the matching item, when found. */
export class NameTakenError extends Error {
  readonly itemName: string;
  readonly existing: CatalogItem | null;
  constructor(itemName: string, existing: CatalogItem | null) {
    super(`An item named ${itemName} already exists`);
    this.name = 'NameTakenError';
    this.itemName = itemName;
    this.existing = existing;
  }
}

type SaveVariables = {
  householdId: string;
  ids: WriteIds;
  /** The catalog item to add an entry of, or null to create `newItem` first. */
  catalogItemId: string | null;
  newItem: { name: string; categoryId: number; locationId: number; shelfLifeDays: number | null } | null;
  level: StockLevel;
  locationId: number;
  purchasedOn: string;
  /** '' for no expiry. */
  expiresOn: string;
  /** Called as soon as a new item exists, so a failing entry create never creates it twice. */
  onItemCreated: (item: CatalogItem) => void;
};

/** A whole number of days from 1 to 3650, or null for anything else. */
function parseShelfLife(text: string): number | null {
  if (!/^\d+$/.test(text.trim())) return null;
  const days = Number(text.trim());
  return days >= MIN_SHELF_LIFE_DAYS && days <= MAX_SHELF_LIFE_DAYS ? days : null;
}

function saveErrorMessage(error: unknown): string {
  if (error instanceof NameTakenError) return `An item named ${error.itemName} already exists`;
  return userMessage(error);
}

type AddItemFormProps = {
  /** Called after a save with the confirmation to show ("Added Milk to Fridge"). */
  onSaved: (message: string) => void;
  confirmation?: string | null;
};

/**
 * The Add item form: the Search step, then the Details step, on the same
 * route. The route remounts it (with a new `key`) on every visit and after
 * every save, so it always starts empty with the name field focused.
 */
export function AddItemForm({ onSaved, confirmation }: AddItemFormProps) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const queryClient = useQueryClient();
  const keys = useHouseholdKeys();
  const householdId = keys.householdId;
  const household = useOptionalCurrentHousehold();
  const householdName = household?.name ?? '';

  const locations = useLocationsQuery();
  const categories = useCategoriesQuery();
  // Read from the cache only: the badge is left out when Pantry is not cached.
  const pantry = useQuery({
    queryKey: keys.pantry(),
    queryFn: ({ signal }) => fetchPantry(householdId, signal),
    enabled: false,
  });
  const pantryCount = useMemo(() => {
    const counts = pantry.data ? new Map(pantry.data.groups.map((group) => [group.catalogItemId, group.entries.length])) : null;
    return (catalogItemId: string) => (counts ? (counts.get(catalogItemId) ?? 0) : null);
  }, [pantry.data]);

  const [step, setStep] = useState<'search' | 'details'>('search');
  const [text, setText] = useState('');
  const [picked, setPicked] = useState<PickedItem | null>(null);
  const [ids, setIds] = useState<WriteIds>(newWriteIds);
  const [level, setLevel] = useState<StockLevel>('full');
  const [locationId, setLocationId] = useState<number | null>(null);
  const [categoryId, setCategoryId] = useState<number | null>(null);
  const [shelfLifeText, setShelfLifeText] = useState('');
  const [purchasedOn, setPurchasedOn] = useState(() => todayOnDevice());
  const [expiry, setExpiry] = useState<Expiry>({ mode: 'suggested' });
  const [notice, setNotice] = useState<string | null>(null);
  // Set when an item is picked with Enter (web), so focus moves to Add.
  const focusAddNext = useRef(false);
  const addButton = useRef<ComponentRef<typeof Pressable>>(null);

  const isNew = picked?.kind === 'new';
  const itemName = picked ? (picked.kind === 'catalog' ? picked.item.name : picked.name) : '';
  const shelfLifeDays = picked?.kind === 'catalog' ? picked.item.shelfLifeDays : parseShelfLife(shelfLifeText);
  const suggestedExpiry =
    shelfLifeDays && isValidCalendarDate(purchasedOn) ? addDays(purchasedOn, shelfLifeDays) : null;
  const expiresOn = expiry.mode === 'suggested' ? (suggestedExpiry ?? '') : expiry.value;

  const save = useMutation({
    mutationKey: [...queryKeys.households.pantry(householdId), 'create'],
    mutationFn: async (variables: SaveVariables): Promise<PantryEntry> => {
      const delay = queryRetryDelay(queryClient);
      let catalogItemId = variables.catalogItemId;
      if (variables.newItem) {
        const { name, categoryId: newCategoryId, locationId: defaultLocationId, shelfLifeDays: days } = variables.newItem;
        let item: CatalogItem;
        try {
          item = await withRetries(
            () =>
              createCatalogItem(variables.householdId, {
                name,
                categoryId: newCategoryId,
                locationId: defaultLocationId,
                ...(days ? { shelfLifeDays: days } : {}),
              }),
            delay,
          );
        } catch (error) {
          if (isApiError(error) && error.status === 409 && error.code === 'name_taken') {
            const query = name.trim();
            const results = await queryClient
              .fetchQuery({
                queryKey: queryKeys.households.catalogSearch(variables.householdId, query),
                queryFn: ({ signal }) => searchCatalog(variables.householdId, query, signal),
                staleTime: 0,
              })
              .catch(() => [] as CatalogItem[]);
            throw new NameTakenError(name, results.find((result) => sameItemName(result.name, name)) ?? null);
          }
          throw error;
        }
        void queryClient.invalidateQueries({ queryKey: catalogQueriesKey(variables.householdId) });
        variables.onItemCreated(item);
        catalogItemId = item.id;
      }
      return withRetries(
        () =>
          createEntry(variables.householdId, {
            id: variables.ids.id,
            eventId: variables.ids.eventId,
            catalogItemId: catalogItemId as string,
            level: variables.level,
            locationId: variables.locationId,
            purchasedOn: variables.purchasedOn,
            // Always sent: leaving it out would make the API fill in its own expiry.
            expiresOn: variables.expiresOn === '' ? null : variables.expiresOn,
          }),
        delay,
      );
    },
    // Runs even when the form has gone (the household was switched): the
    // pantry the entry was added to is refreshed either way.
    onSuccess: (_entry, variables) => {
      void queryClient.invalidateQueries({ queryKey: queryKeys.households.pantry(variables.householdId) });
    },
  });

  /**
   * Switches to an existing catalog item, keeping the level, location and
   * dates the user chose (the form cannot change while it is saving, so
   * `expiresOn` is still what was sent).
   */
  const switchToItem = (item: CatalogItem) => {
    setPicked({ kind: 'catalog', item });
    setExpiry({ mode: 'manual', value: expiresOn });
  };

  const startDetails = (next: PickedItem, defaultLocation: number | null, byKeyboard: boolean) => {
    save.reset();
    setPicked(next);
    setIds(newWriteIds());
    setLevel('full');
    setLocationId(defaultLocation);
    setCategoryId(null);
    setShelfLifeText('');
    setPurchasedOn(todayOnDevice());
    setExpiry({ mode: 'suggested' });
    setNotice(null);
    setStep('details');
    focusAddNext.current = byKeyboard;
  };

  // On web, an item picked with Enter moves focus to Add, so a second Enter adds it.
  useEffect(() => {
    if (step === 'details' && focusAddNext.current) {
      focusAddNext.current = false;
      focusElement(addButton.current);
    }
  }, [step, picked]);

  const backToSearch = () => {
    save.reset();
    setNotice(null);
    setStep('search');
  };

  const locationsReady = locations.isSuccess;
  const categoriesReady = categories.isSuccess;
  const boughtError = isValidCalendarDate(purchasedOn) ? null : 'Enter the date you bought it';
  const categoryError = isNew && categoryId === null ? 'Choose a category' : null;
  const locationError = locationId === null ? 'Choose a location' : null;
  const shelfLifeError =
    isNew && shelfLifeText.trim() !== '' && parseShelfLife(shelfLifeText) === null
      ? `Enter a whole number of days from ${MIN_SHELF_LIFE_DAYS} to ${MAX_SHELF_LIFE_DAYS}`
      : null;
  const expiryError = expiresOn !== '' && !isValidCalendarDate(expiresOn) ? 'Enter a valid expiry date' : null;
  const canSave =
    !!picked &&
    locationsReady &&
    categoriesReady &&
    !boughtError &&
    !categoryError &&
    !locationError &&
    !shelfLifeError &&
    !expiryError;

  const submit = () => {
    if (!picked || !canSave || save.isPending || locationId === null) return;
    const variables: SaveVariables = {
      householdId,
      ids,
      catalogItemId: picked.kind === 'catalog' ? picked.item.id : null,
      newItem:
        picked.kind === 'new' && categoryId !== null
          ? { name: picked.name, categoryId, locationId, shelfLifeDays: parseShelfLife(shelfLifeText) }
          : null,
      level,
      locationId,
      purchasedOn,
      expiresOn,
      onItemCreated: switchToItem,
    };
    setNotice(null);
    save.mutate(variables, {
      onSuccess: (entry) => {
        // Not called once the form has gone: switching household remounts it
        // (its key has the household ID), so nothing is confirmed under the new household.
        const where = locations.data?.find((location) => location.id === entry.locationId)?.name;
        onSaved(where ? `Added ${itemName} to ${where}` : `Added ${itemName}`);
      },
      onError: (error) => {
        if (error instanceof NameTakenError && error.existing) {
          switchToItem(error.existing);
          setNotice(`${error.existing.name} already exists, so we used it`);
          save.reset();
          return;
        }
        if (isApiError(error) && error.status === 409 && error.code && ID_CONFLICT_CODES.includes(error.code)) {
          setIds(newWriteIds());
        }
      },
    });
  };

  if (step === 'search' || !picked) {
    return (
      <AddItemSearch
        text={text}
        onChangeText={setText}
        householdName={householdName}
        pantryCount={pantryCount}
        confirmation={confirmation}
        onPickItem={(item, byKeyboard) => startDetails({ kind: 'catalog', item }, item.locationId, byKeyboard)}
        onPickNew={(name, byKeyboard) => startDetails({ kind: 'new', name: cleanItemName(name) }, null, byKeyboard)}
      />
    );
  }

  const count = picked.kind === 'catalog' ? pantryCount(picked.item.id) : null;
  const categoryName =
    picked.kind === 'catalog' ? categories.data?.find((category) => category.id === picked.item.categoryId)?.name : undefined;
  const showHint = !!shelfLifeDays && expiresOn !== '' && expiresOn === suggestedExpiry;
  const busy = save.isPending;

  return (
    <View style={styles.root}>
      <ScrollView style={styles.scroll} contentContainerStyle={styles.details} keyboardShouldPersistTaps="handled">
        {notice ? <FormMessage tone="info" message={notice} testID="add-notice" /> : null}
        <View style={styles.summary} testID="add-summary">
          <View style={styles.summaryText}>
            <Text style={styles.summaryName}>{itemName}</Text>
            <Text style={styles.muted}>
              {picked.kind === 'new'
                ? `New item, just for ${householdName}`
                : [categoryName, count ? `already ${count} in the pantry` : null].filter(Boolean).join(' · ')}
            </Text>
          </View>
          <Button variant="link" label="Change" onPress={backToSearch} disabled={busy} testID="add-change" />
        </View>

        {locations.isError ? (
          <FormMessage tone="error" message="Couldn't load locations" testID="add-locations-error">
            <Button variant="link" label="Retry" onPress={() => void locations.refetch()} testID="add-locations-retry" />
          </FormMessage>
        ) : null}
        {categories.isError ? (
          <FormMessage tone="error" message="Couldn't load categories" testID="add-categories-error">
            <Button variant="link" label="Retry" onPress={() => void categories.refetch()} testID="add-categories-retry" />
          </FormMessage>
        ) : null}
        {(locations.isPending && !locations.isError) || (categories.isPending && !categories.isError) ? (
          <ActivityIndicator testID="add-details-loading" color={theme.colors.primary} />
        ) : null}

        {picked.kind === 'new' && categories.data ? (
          <ChoiceChips
            label="Category"
            testID="add-category"
            options={categories.data.map((category) => ({ value: category.id, label: category.name }))}
            value={categoryId}
            onChange={setCategoryId}
            error={categoryError}
            disabled={busy}
          />
        ) : null}

        <LevelPicker value={level} onChange={setLevel} disabled={busy} />

        {locations.data ? (
          <ChoiceChips
            label="Where is it?"
            testID="add-location"
            options={locations.data.map((location) => ({ value: location.id, label: location.name }))}
            value={locationId}
            onChange={setLocationId}
            error={locationError}
            disabled={busy}
          />
        ) : null}

        {picked.kind === 'new' ? (
          <TextField
            label="Usually keeps for … days (optional)"
            testID="add-shelf-life"
            value={shelfLifeText}
            onChangeText={setShelfLifeText}
            keyboardType="number-pad"
            inputMode="numeric"
            maxLength={4}
            error={shelfLifeError}
            editable={!busy}
          />
        ) : null}

        <DateField
          label="Bought"
          testID="add-bought"
          value={purchasedOn}
          onChange={setPurchasedOn}
          error={boughtError}
          disabled={busy}
        />
        <DateField
          label="Expires (optional)"
          testID="add-expires"
          value={expiresOn}
          onChange={(value) => setExpiry({ mode: 'manual', value })}
          clearLabel="Clear expiry date"
          error={expiryError}
          hint={showHint ? `Suggested from ${itemName}'s typical shelf life (${shelfLifeDays} days)` : null}
          disabled={busy}
        />
      </ScrollView>

      <View style={styles.footer}>
        {save.isError ? <FormMessage tone="error" message={saveErrorMessage(save.error)} testID="add-error" /> : null}
        <Button
          ref={addButton}
          testID="add-submit"
          label={`Add ${itemName}`}
          busy={busy}
          disabled={!canSave}
          onPress={submit}
        />
      </View>
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    root: { flex: 1 },
    scroll: { flex: 1 },
    details: { gap: theme.spacing.lg, paddingBottom: theme.spacing.lg },
    summary: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.md,
      padding: theme.spacing.lg,
      borderRadius: theme.radii.card,
      borderWidth: 1,
      borderColor: theme.colors.border,
      backgroundColor: theme.colors.surface,
    },
    summaryText: { flex: 1, gap: theme.spacing.xs },
    summaryName: { ...theme.typography.title, fontSize: 24, lineHeight: 30, color: theme.colors.text },
    muted: { ...theme.typography.caption, color: theme.colors.mutedText },
    footer: {
      gap: theme.spacing.sm,
      paddingTop: theme.spacing.md,
      paddingBottom: theme.spacing.lg,
      borderTopWidth: 1,
      borderTopColor: theme.colors.border,
      backgroundColor: theme.colors.background,
    },
  };
}

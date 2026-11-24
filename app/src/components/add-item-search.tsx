import { useEffect, useMemo, useState } from 'react';
import {
  ActivityIndicator,
  Pressable,
  ScrollView,
  Text,
  View,
  type TextInputKeyPressEvent,
} from 'react-native';

import {
  cleanItemName,
  MAX_ITEM_NAME_LENGTH,
  MAX_SEARCH_LENGTH,
  sameItemName,
  useCatalogSearchQuery,
  type CatalogItem,
} from '@/catalog/catalog';
import { useCategoryNames, useLocationNames } from '@/pantry/pantry';
import { useTheme, useThemedStyles, type Theme } from '@/theme';

import { usesKeyboardHighlight } from './add-item-keyboard';
import { FormMessage } from './form-message';
import { TextField } from './text-field';

/** How long typing has to pause before the catalog is searched. */
export const SEARCH_DEBOUNCE_MS = 250;

/** `value`, but only once it has stayed the same for `delayMs`. */
export function useDebouncedValue<T>(value: T, delayMs: number): T {
  const [debounced, setDebounced] = useState(value);
  useEffect(() => {
    const timer = setTimeout(() => setDebounced(value), delayMs);
    return () => clearTimeout(timer);
  }, [value, delayMs]);
  return debounced;
}

type AddItemSearchProps = {
  text: string;
  onChangeText: (text: string) => void;
  /** A catalog suggestion was picked; `byKeyboard` when it was picked with Enter. */
  onPickItem: (item: CatalogItem, byKeyboard: boolean) => void;
  /** The Create option was picked, with the cleaned name. */
  onPickNew: (name: string, byKeyboard: boolean) => void;
  householdName: string;
  /** How many entries of an item the cached pantry has, or null when the pantry is not cached. */
  pantryCount: (catalogItemId: string) => number | null;
  /** Shown above the field, e.g. "Added Milk to Fridge" after a save. */
  confirmation?: string | null;
};

type Option = { kind: 'item'; item: CatalogItem } | { kind: 'create' };

/** Add item's first step: the name field, its type-ahead suggestions and the Create option. */
export function AddItemSearch({
  text,
  onChangeText,
  onPickItem,
  onPickNew,
  householdName,
  pantryCount,
  confirmation,
}: AddItemSearchProps) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const trimmed = text.trim();
  const query = useDebouncedValue(trimmed, SEARCH_DEBOUNCE_MS);
  const search = useCatalogSearchQuery(query);
  const categoryName = useCategoryNames();
  const locationName = useLocationNames();
  const keyboard = usesKeyboardHighlight();

  const suggestions = useMemo(
    () => (trimmed === '' || query === '' ? [] : (search.data ?? [])),
    [trimmed, query, search.data],
  );
  // The latest search is the one for the text now in the field, and it has finished or failed.
  const latestSettled =
    trimmed !== '' && query === trimmed && ((search.isSuccess && !search.isPlaceholderData) || search.isError);
  const cleaned = cleanItemName(text);
  const showCreate = latestSettled && !suggestions.some((item) => sameItemName(item.name, cleaned));
  const createDisabled = cleaned.length > MAX_ITEM_NAME_LENGTH;
  const loading = query !== '' && trimmed !== '' && search.isFetching;

  const options: Option[] = useMemo(() => {
    const list: Option[] = suggestions.map((item) => ({ kind: 'item', item }));
    if (showCreate && !createDisabled) list.push({ kind: 'create' });
    return list;
  }, [suggestions, showCreate, createDisabled]);

  // The first option is highlighted whenever new results arrive (a new search,
  // or different options), but not when the same results are fetched again.
  // Only the keyboard moves the highlight: hovering has its own style, because
  // browsers report a hover when results appear under a pointer that has not
  // moved, and Enter must still pick the first suggestion then.
  const optionsKey = `${query}#${options.map((option) => (option.kind === 'item' ? option.item.id : 'create')).join('|')}`;
  const [highlightState, setHighlightState] = useState({ optionsKey, index: 0 });
  const highlight = highlightState.optionsKey === optionsKey ? highlightState.index : 0;
  const setHighlight = (next: number | ((current: number) => number)) =>
    setHighlightState({ optionsKey, index: typeof next === 'function' ? next(highlight) : next });

  const pick = (option: Option | undefined, byKeyboard: boolean) => {
    if (!option) return;
    if (option.kind === 'item') onPickItem(option.item, byKeyboard);
    else onPickNew(cleaned, byKeyboard);
  };

  const onKeyPress = (event: TextInputKeyPressEvent) => {
    if (!keyboard || options.length === 0) return;
    const { key } = event.nativeEvent;
    if (key !== 'ArrowDown' && key !== 'ArrowUp') return;
    (event as { preventDefault?: () => void }).preventDefault?.();
    setHighlight((current) => {
      const next = key === 'ArrowDown' ? current + 1 : current - 1;
      return (next + options.length) % options.length;
    });
  };

  const highlighted = keyboard ? options[Math.min(highlight, options.length - 1)] : undefined;
  // Which option the pointer is over (web), for its hover style only.
  const [hovered, setHovered] = useState<string | null>(null);
  const hoverProps = (id: string) =>
    keyboard
      ? {
          onHoverIn: () => setHovered(id),
          onHoverOut: () => setHovered((current) => (current === id ? null : current)),
        }
      : {};

  return (
    <View style={styles.root}>
      {confirmation && trimmed === '' ? <FormMessage tone="info" message={confirmation} testID="add-confirmation" /> : null}
      <TextField
        label="Item name"
        testID="add-item-name"
        placeholder="Search or add an item"
        value={text}
        onChangeText={onChangeText}
        maxLength={MAX_SEARCH_LENGTH}
        autoFocus
        autoCapitalize="sentences"
        autoCorrect={false}
        returnKeyType="next"
        submitBehavior="submit"
        onKeyPress={onKeyPress}
        onSubmitEditing={() => pick(highlighted, true)}
        accessibilityRole="search"
      />
      <ScrollView style={styles.results} keyboardShouldPersistTaps="handled" contentContainerStyle={styles.resultsContent}>
        {loading ? (
          <View style={styles.loading}>
            <ActivityIndicator testID="add-search-loading" size="small" color={theme.colors.primary} />
            <Text style={styles.muted}>Searching…</Text>
          </View>
        ) : null}
        {search.isError && trimmed !== '' ? (
          <FormMessage tone="error" message="Couldn't load suggestions" testID="add-search-error" />
        ) : null}
        <View accessibilityLabel="Suggestions" style={styles.list}>
          {suggestions.map((item) => {
            const category = categoryName(item.categoryId) ?? '';
            const location = locationName(item.locationId) ?? '';
            const count = pantryCount(item.id);
            const badges = [item.isCustom ? 'Household item' : null, count ? `${count} in pantry` : null].filter(
              (badge): badge is string => badge !== null,
            );
            const isHighlighted = highlighted?.kind === 'item' && highlighted.item.id === item.id;
            return (
              <Pressable
                key={item.id}
                testID={`add-suggestion-${item.id}`}
                accessibilityRole="button"
                accessibilityLabel={[item.name, category, location, ...badges].filter(Boolean).join(', ')}
                onPress={() => pick({ kind: 'item', item }, false)}
                {...hoverProps(item.id)}
                style={[
                  styles.option,
                  hovered === item.id ? styles.optionHovered : null,
                  isHighlighted ? styles.optionHighlighted : null,
                ]}
              >
                <View style={styles.optionText}>
                  <HighlightedName name={item.name} typed={trimmed} />
                  <Text style={styles.muted}>{[category, location].filter(Boolean).join(' · ')}</Text>
                </View>
                {badges.length > 0 ? (
                  <View style={styles.badges}>
                    {badges.map((badge) => (
                      <Text key={badge} style={styles.badge}>
                        {badge}
                      </Text>
                    ))}
                  </View>
                ) : null}
              </Pressable>
            );
          })}
          {showCreate ? (
            <Pressable
              testID="add-create"
              accessibilityRole="button"
              accessibilityLabel={
                createDisabled
                  ? `Create “${cleaned}”, Names can be at most ${MAX_ITEM_NAME_LENGTH} characters`
                  : `Create “${cleaned}”, New item, just for ${householdName}`
              }
              accessibilityState={{ disabled: createDisabled }}
              disabled={createDisabled}
              onPress={() => pick({ kind: 'create' }, false)}
              {...(createDisabled ? {} : hoverProps('create'))}
              style={[
                styles.option,
                styles.create,
                hovered === 'create' ? styles.optionHovered : null,
                highlighted?.kind === 'create' ? styles.optionHighlighted : null,
                createDisabled ? styles.disabled : null,
              ]}
            >
              <View style={styles.optionText}>
                <Text style={styles.createName} numberOfLines={2}>
                  Create “{cleaned}”
                </Text>
                <Text style={createDisabled ? styles.errorText : styles.muted}>
                  {createDisabled
                    ? `Names can be at most ${MAX_ITEM_NAME_LENGTH} characters`
                    : `New item, just for ${householdName}`}
                </Text>
              </View>
            </Pressable>
          ) : null}
        </View>
      </ScrollView>
    </View>
  );
}

/** The item's name with the first case-insensitive match of the typed text in bold. */
function HighlightedName({ name, typed }: { name: string; typed: string }) {
  const styles = useThemedStyles(createStyles);
  const index = typed ? name.toLowerCase().indexOf(typed.toLowerCase()) : -1;
  if (index < 0) return <Text style={styles.name}>{name}</Text>;
  return (
    <Text style={styles.name}>
      {name.slice(0, index)}
      <Text style={styles.match} testID="add-suggestion-match">
        {name.slice(index, index + typed.length)}
      </Text>
      {name.slice(index + typed.length)}
    </Text>
  );
}

function createStyles(theme: Theme) {
  return {
    root: { flex: 1, gap: theme.spacing.md },
    results: { flex: 1 },
    resultsContent: { gap: theme.spacing.sm, paddingBottom: theme.spacing.xl },
    loading: { flexDirection: 'row' as const, alignItems: 'center' as const, gap: theme.spacing.sm },
    list: { gap: theme.spacing.sm },
    option: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.sm,
      minHeight: 56,
      paddingHorizontal: theme.spacing.lg,
      paddingVertical: theme.spacing.sm,
      borderRadius: theme.radii.card,
      borderWidth: 1,
      borderColor: theme.colors.border,
      backgroundColor: theme.colors.surface,
    },
    optionHovered: { borderColor: theme.colors.primary },
    optionHighlighted: { borderColor: theme.colors.primary, backgroundColor: theme.colors.primarySubtle },
    create: { borderStyle: 'dashed' as const },
    disabled: { opacity: 0.7 },
    optionText: { flex: 1, gap: 2 },
    name: { ...theme.typography.body, color: theme.colors.text },
    match: { fontFamily: theme.typography.title.fontFamily, fontWeight: theme.typography.title.fontWeight },
    createName: { ...theme.typography.label, color: theme.colors.primary },
    muted: { ...theme.typography.caption, color: theme.colors.mutedText },
    errorText: { ...theme.typography.caption, color: theme.colors.danger },
    badges: { alignItems: 'flex-end' as const, gap: theme.spacing.xs },
    badge: {
      ...theme.typography.caption,
      color: theme.colors.primary,
      backgroundColor: theme.colors.primarySubtle,
      borderRadius: theme.radii.pill,
      paddingHorizontal: theme.spacing.sm,
      overflow: 'hidden' as const,
    },
  };
}

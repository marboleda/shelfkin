// The controls above the Pantry list: the "Group by" Location / Category
// segmented control, the Filters button and its panel of location and category
// chips, and the "Filtered: …" line that stays visible while any filter is on.

import Feather from '@expo/vector-icons/Feather';
import { Pressable, Text, View } from 'react-native';

import type { Category, Location } from '@/pantry/pantry';
import { activeFilterCount, filterNames, toggleId, type PantryFilters, type ViewMode } from '@/pantry/sections';
import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

import { Button } from './button';
import { ThemedText } from './themed-text';

const MODE_OPTIONS: { mode: ViewMode; label: string }[] = [
  { mode: 'location', label: 'Location' },
  { mode: 'category', label: 'Category' },
];

type PantryToolbarProps = {
  mode: ViewMode;
  onModeChange: (mode: ViewMode) => void;
  filters: PantryFilters;
  panelOpen: boolean;
  onTogglePanel: () => void;
  /** At 768 px and wider the segmented control is sized to its options instead of filling the line. */
  wide?: boolean;
};

/** "Group by" and the Filters button, on one line. */
export function PantryToolbar({ mode, onModeChange, filters, panelOpen, onTogglePanel, wide = false }: PantryToolbarProps) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const count = activeFilterCount(filters);
  const label = count > 0 ? `Filters · ${count}` : 'Filters';

  return (
    <View style={styles.toolbar}>
      <View
        role="radiogroup"
        aria-label="Group by"
        accessibilityLabel="Group by"
        testID="pantry-group-by"
        style={[styles.segmented, wide ? styles.segmentedCompact : null]}
      >
        {MODE_OPTIONS.map((option) => {
          const selected = option.mode === mode;
          return (
            <Pressable
              key={option.mode}
              testID={`pantry-group-by-${option.mode}`}
              role="radio"
              aria-checked={selected}
              aria-selected={selected}
              accessibilityState={{ checked: selected, selected }}
              aria-label={option.label}
              onPress={() => {
                if (!selected) onModeChange(option.mode);
              }}
              style={[styles.segment, selected ? styles.segmentSelected : null]}
            >
              <Text style={[styles.segmentLabel, selected ? styles.segmentLabelSelected : null]}>{option.label}</Text>
            </Pressable>
          );
        })}
      </View>
      <Pressable
        testID="pantry-filters-button"
        accessibilityRole="button"
        aria-label={label}
        aria-expanded={panelOpen}
        accessibilityState={{ expanded: panelOpen }}
        accessibilityHint={panelOpen ? 'Hides the filters' : 'Shows the filters'}
        onPress={onTogglePanel}
        style={[styles.filtersButton, count > 0 ? styles.filtersButtonActive : null]}
      >
        <Feather name="sliders" size={16} color={count > 0 ? theme.colors.primary : theme.colors.text} aria-hidden />
        <Text style={styles.filtersLabel}>{label}</Text>
      </Pressable>
    </View>
  );
}

type FilterPanelProps = {
  locations: readonly Location[];
  categories: readonly Category[];
  filters: PantryFilters;
  onChange: (filters: PantryFilters) => void;
};

/** The location and category chips. Chips wrap, so every one is reachable without scrolling sideways. */
export function FilterPanel({ locations, categories, filters, onChange }: FilterPanelProps) {
  const styles = useThemedStyles(createStyles);
  return (
    <View testID="pantry-filter-panel" style={styles.panel}>
      <ChipGroup
        title="Location"
        options={locations}
        selected={filters.locationIds}
        onToggle={(id) => onChange({ ...filters, locationIds: toggleId(filters.locationIds, id) })}
      />
      <ChipGroup
        title="Category"
        options={categories}
        selected={filters.categoryIds}
        onToggle={(id) => onChange({ ...filters, categoryIds: toggleId(filters.categoryIds, id) })}
      />
    </View>
  );
}

function ChipGroup({
  title,
  options,
  selected,
  onToggle,
}: {
  title: string;
  options: readonly { id: number; name: string }[];
  selected: readonly number[];
  onToggle: (id: number) => void;
}) {
  const styles = useThemedStyles(createStyles);
  return (
    <View role="group" aria-label={title} accessibilityLabel={title} style={styles.chipGroup}>
      <Text style={styles.groupTitle} aria-hidden>
        {title}
      </Text>
      <View style={styles.chips}>
        {options.map((option) => (
          <FilterChip
            key={option.id}
            testID={`pantry-filter-${title.toLowerCase()}-${option.id}`}
            label={option.name}
            selected={selected.includes(option.id)}
            onPress={() => onToggle(option.id)}
          />
        ))}
      </View>
    </View>
  );
}

function FilterChip({
  label,
  selected,
  onPress,
  testID,
}: {
  label: string;
  selected: boolean;
  onPress: () => void;
  testID: string;
}) {
  const styles = useThemedStyles(createStyles);
  return (
    <Pressable
      testID={testID}
      role="checkbox"
      aria-checked={selected}
      aria-selected={selected}
      accessibilityState={{ checked: selected, selected }}
      aria-label={label}
      onPress={onPress}
      style={[styles.chip, selected ? styles.chipSelected : null]}
    >
      <Text style={styles.chipLabel}>{label}</Text>
    </Pressable>
  );
}

type FilterLineProps = {
  locations: readonly Location[];
  categories: readonly Category[];
  filters: PantryFilters;
  onClear: () => void;
};

/** "Filtered: Fridge, Dairy" with Clear, shown while any filter is on, panel open or not. */
export function FilterLine({ locations, categories, filters, onClear }: FilterLineProps) {
  const styles = useThemedStyles(createStyles);
  const names = filterNames(filters, locations, categories);
  if (names.length === 0) return null;
  return (
    <View testID="pantry-filter-line" style={styles.filterLine}>
      <ThemedText variant="muted" style={styles.filterLineText}>
        Filtered: {names.join(', ')}
      </ThemedText>
      <Button variant="link" label="Clear" onPress={onClear} />
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    toolbar: {
      flexDirection: 'row' as const,
      flexWrap: 'wrap' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.sm,
    },
    segmented: {
      flex: 1,
      minWidth: 200,
      flexDirection: 'row' as const,
      padding: 3,
      gap: 3,
      borderRadius: theme.radii.md,
      borderWidth: 1,
      borderColor: theme.colors.border,
      backgroundColor: theme.colors.background,
    },
    // `flex: 1` above sets a zero basis, so the basis is reset for the fixed width to apply.
    segmentedCompact: {
      flexGrow: 0,
      flexShrink: 1,
      flexBasis: 'auto' as const,
      width: 280,
    },
    segment: {
      flex: 1,
      minHeight: MIN_TOUCH_TARGET,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      paddingHorizontal: theme.spacing.sm,
      borderRadius: 8,
      borderWidth: 1,
      // The track's colour, so selecting a segment does not shift its label.
      borderColor: theme.colors.background,
    },
    segmentSelected: {
      backgroundColor: theme.colors.surface,
      borderColor: theme.colors.border,
    },
    segmentLabel: {
      ...theme.typography.label,
      fontSize: 14,
      color: theme.colors.mutedText,
    },
    segmentLabelSelected: {
      color: theme.colors.text,
    },
    filtersButton: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: 6,
      minHeight: MIN_TOUCH_TARGET + 8,
      paddingHorizontal: theme.spacing.md,
      borderRadius: theme.radii.pill,
      borderWidth: 1,
      borderColor: theme.colors.border,
      backgroundColor: theme.colors.surface,
    },
    filtersButtonActive: {
      borderColor: theme.colors.primary,
      backgroundColor: theme.colors.primarySubtle,
    },
    filtersLabel: {
      ...theme.typography.label,
      fontSize: 14,
      color: theme.colors.text,
    },
    panel: {
      gap: theme.spacing.md,
      padding: theme.spacing.md,
      borderRadius: theme.radii.card,
      borderWidth: 1,
      borderColor: theme.colors.border,
      backgroundColor: theme.colors.surface,
    },
    chipGroup: {
      gap: theme.spacing.sm,
    },
    groupTitle: {
      ...theme.typography.label,
      color: theme.colors.text,
    },
    chips: {
      flexDirection: 'row' as const,
      flexWrap: 'wrap' as const,
      gap: theme.spacing.sm,
    },
    chip: {
      minHeight: MIN_TOUCH_TARGET,
      justifyContent: 'center' as const,
      paddingHorizontal: theme.spacing.lg,
      borderRadius: theme.radii.pill,
      borderWidth: 1,
      borderColor: theme.colors.border,
      backgroundColor: theme.colors.surface,
    },
    chipSelected: {
      borderColor: theme.colors.primary,
      backgroundColor: theme.colors.primarySubtle,
    },
    chipLabel: {
      ...theme.typography.label,
      color: theme.colors.text,
    },
    filterLine: {
      flexDirection: 'row' as const,
      flexWrap: 'wrap' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.sm,
    },
    filterLineText: {
      ...theme.typography.caption,
      flexShrink: 1,
    },
  };
}

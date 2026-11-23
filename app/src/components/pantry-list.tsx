// The Pantry list, in sections by location or by category. A row is one item
// ("Milk ×2") with the entries shown in that section. A row with one entry
// shows that entry and opens it; a row with several expands to show each
// entry, and while collapsed summarises them.
//
// The list is a SectionList, so only the rows near the screen are rendered,
// however large the pantry. One item can appear in several sections (by
// location), so row keys and test IDs include the section key.

import Feather from '@expo/vector-icons/Feather';
import { memo, useCallback, useMemo, useState, type ReactElement } from 'react';
import { Platform, Pressable, SectionList, Text, View } from 'react-native';

import {
  expiryLabel,
  formatCalendarDate,
  groupAccessibilityLabel,
  lastChangeLabel,
  levelLabel,
  summarizeGroup,
  type ExpiryTone,
} from '@/pantry/format';
import type { PantryGroup, PantryListEntry } from '@/pantry/pantry';
import { rowKey, type PantrySection, type ViewMode } from '@/pantry/sections';
import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

import { ExpiryChip } from './expiry-chip';
import { LevelMeter } from './level-meter';
import { PressableLink } from './pressable-link';

/** What every row needs besides its own group, shared so rows only re-render when it changes. */
export type PantryRowContext = {
  /** The pantry's `today` (`YYYY-MM-DD`), which expiry labels are worded against. */
  today: string;
  /** The current time, for "2h ago". */
  now: Date;
  /** The signed-in user's ID, for "by you". */
  currentUserId: string | null;
  locationName: (id: number) => string | undefined;
};

type PantryListProps = PantryRowContext & {
  /** The sections to show, from `buildSections`; `empty` shows when there are none. */
  sections: PantrySection[];
  /** The view the sections are for. Switching it collapses every row. */
  mode: ViewMode;
  /** Rendered above the rows, scrolling with them. */
  header?: ReactElement | null;
  /** Shown below the header instead of the rows when there are no sections. */
  empty?: ReactElement | null;
  /** Space below the last row, e.g. so a floating button never covers it. */
  bottomInset?: number;
};

const NONE_EXPANDED: ReadonlySet<string> = new Set();

export function PantryList({
  sections,
  mode,
  today,
  now,
  currentUserId,
  locationName,
  header,
  empty,
  bottomInset = 0,
}: PantryListProps) {
  const styles = useThemedStyles(createStyles);
  // Expanded rows by row key (section and catalog item), for the current view.
  // Kept across refetches and filter changes (the keys stay the same); another
  // view starts with every row collapsed. Pantry remounts the list when the
  // household changes, which resets it too.
  const [expandedState, setExpandedState] = useState<{ mode: ViewMode; keys: ReadonlySet<string> }>(() => ({
    mode,
    keys: NONE_EXPANDED,
  }));
  if (expandedState.mode !== mode) {
    // Adjusting state while rendering: React re-renders straight away, before anything is shown.
    setExpandedState({ mode, keys: NONE_EXPANDED });
  }
  const expanded = expandedState.mode === mode ? expandedState.keys : NONE_EXPANDED;
  const toggle = useCallback(
    (key: string) => {
      setExpandedState((previous) => {
        const next = new Set(previous.mode === mode ? previous.keys : NONE_EXPANDED);
        if (next.has(key)) next.delete(key);
        else next.add(key);
        return { mode, keys: next };
      });
    },
    [mode],
  );

  const context = useMemo<PantryRowContext>(
    () => ({ today, now, currentUserId, locationName }),
    [today, now, currentUserId, locationName],
  );
  const firstSectionKey = sections[0]?.key;

  return (
    <SectionList
      testID="pantry-list"
      style={styles.list}
      contentContainerStyle={[styles.content, { paddingBottom: bottomInset }]}
      sections={sections}
      keyExtractor={(group) => group.catalogItemId}
      extraData={{ expanded, context }}
      renderItem={({ item, section }) => {
        const key = rowKey(section.key, item.catalogItemId);
        return (
          <PantryGroupRow rowKey={key} group={item} expanded={expanded.has(key)} onToggle={toggle} context={context} />
        );
      }}
      renderSectionHeader={({ section }) => <SectionHeader section={section} first={section.key === firstSectionKey} />}
      stickySectionHeadersEnabled={false}
      ListHeaderComponent={header ?? undefined}
      ListEmptyComponent={empty ?? undefined}
      ItemSeparatorComponent={Separator}
      initialNumToRender={12}
      windowSize={7}
      // Detaches off-screen rows from the native view tree, which keeps long lists smooth on Android.
      removeClippedSubviews={Platform.OS === 'android'}
    />
  );
}

/** "FRIDGE" on the left and the number of rows on the right, announced as a heading. */
function SectionHeader({ section, first }: { section: PantrySection; first: boolean }) {
  const styles = useThemedStyles(createStyles);
  const count = section.data.length;
  return (
    <View
      testID={`pantry-section-${section.key}`}
      role="heading"
      aria-level={2}
      aria-label={`${section.title}, ${count} ${count === 1 ? 'item' : 'items'}`}
      style={[styles.sectionHeader, first ? null : styles.sectionHeaderSpaced]}
    >
      <Text style={styles.sectionTitle}>{section.title}</Text>
      <Text testID="pantry-section-count" style={styles.sectionCount}>
        {count}
      </Text>
    </View>
  );
}

function Separator() {
  const styles = useThemedStyles(createStyles);
  return <View style={styles.separator} />;
}

type PantryGroupRowProps = {
  /** Unique across the list (see `rowKey`): the row's test ID and what `onToggle` gets. */
  rowKey: string;
  /** The item, with only the entries shown in this row. */
  group: PantryGroup;
  expanded: boolean;
  onToggle: (rowKey: string) => void;
  context: PantryRowContext;
};

/** One item: its single entry, or a collapsible row of its entries. */
export const PantryGroupRow = memo(function PantryGroupRow({
  rowKey: key,
  group,
  expanded,
  onToggle,
  context,
}: PantryGroupRowProps) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const entries = group.entries;

  if (entries.length === 1) {
    const entry = entries[0];
    const expiry = expiryLabel(entry, context.today);
    return (
      <View testID={`pantry-row-${key}`} style={[styles.card, highlightStyle(styles, expiry.tone)]}>
        <PressableLink
          href={{ pathname: '/entries/[id]', params: { id: entry.id } }}
          accessibilityRole="link"
          accessibilityLabel={entryAccessibilityLabel(group.name, entry, context)}
          style={styles.row}
        >
          <View style={styles.rowText}>
            <Text numberOfLines={2} style={[styles.name, entry.level === 'out' ? styles.mutedText : null]}>
              {group.name}
            </Text>
            <EntryDetails entry={entry} context={context} />
          </View>
          <LevelMeter level={entry.level} />
        </PressableLink>
      </View>
    );
  }

  const summary = summarizeGroup(entries, context.today);
  const tone = summary.highlighted ? summary.expiry?.tone : undefined;
  return (
    <View testID={`pantry-row-${key}`} style={[styles.card, highlightStyle(styles, tone)]}>
      <Pressable
        onPress={() => onToggle(key)}
        accessibilityRole="button"
        accessibilityLabel={groupAccessibilityLabel(group.name, entries, context.today)}
        accessibilityHint={expanded ? 'Hides the entries' : 'Shows each entry'}
        accessibilityState={{ expanded }}
        aria-expanded={expanded}
        style={styles.row}
      >
        <View style={styles.rowText}>
          <View style={styles.nameLine}>
            <Text numberOfLines={2} style={[styles.name, styles.groupName, summary.allOut ? styles.mutedText : null]}>
              {group.name}
            </Text>
            <View style={styles.countBadge}>
              <Text testID="pantry-row-count" style={styles.countText}>
                ×{entries.length}
              </Text>
            </View>
          </View>
          {expanded ? null : (
            <View style={styles.detailLine}>
              <Text style={[styles.caption, summary.allOut ? styles.mutedText : null]}>{summary.levels}</Text>
              {summary.expiry ? <ExpiryChip label={summary.expiry} /> : null}
            </View>
          )}
        </View>
        <Feather
          name={expanded ? 'chevron-up' : 'chevron-down'}
          size={20}
          color={theme.colors.mutedText}
          aria-hidden
        />
      </Pressable>
      {expanded
        ? entries.map((entry) => (
            <PressableLink
              key={entry.id}
              testID={`pantry-entry-${entry.id}`}
              href={{ pathname: '/entries/[id]', params: { id: entry.id } }}
              accessibilityRole="link"
              accessibilityLabel={entryAccessibilityLabel(group.name, entry, context)}
              style={[styles.row, styles.entryRow]}
            >
              <View style={styles.rowText}>
                <EntryDetails entry={entry} context={context} />
              </View>
              <LevelMeter level={entry.level} />
            </PressableLink>
          ))
        : null}
    </View>
  );
});

/** An entry's purchase date and location, expiry, and last change. */
function EntryDetails({ entry, context }: { entry: PantryListEntry; context: PantryRowContext }) {
  const styles = useThemedStyles(createStyles);
  const muted = entry.level === 'out';
  const location = context.locationName(entry.locationId);
  const bought = `Bought ${formatCalendarDate(entry.purchasedOn, context.today)}`;
  const change = lastChangeLabel(entry.lastChange, {
    currentUserId: context.currentUserId,
    locationName: context.locationName,
    now: context.now,
  });

  return (
    <>
      <Text style={[styles.caption, muted ? styles.mutedText : styles.bodyText]}>
        {location ? `${bought} · ${location}` : bought}
      </Text>
      <ExpiryChip label={expiryLabel(entry, context.today)} />
      {change ? (
        <Text testID="last-change" style={styles.caption}>
          {change}
        </Text>
      ) : null}
    </>
  );
}

/** What a screen reader says for one entry, e.g. "Milk, Low, bought Oct 20, Fridge, expires tomorrow, Set to Low by Sam, 2h ago". */
export function entryAccessibilityLabel(name: string, entry: PantryListEntry, context: PantryRowContext): string {
  const location = context.locationName(entry.locationId);
  const change = lastChangeLabel(entry.lastChange, {
    currentUserId: context.currentUserId,
    locationName: context.locationName,
    now: context.now,
  });
  return [
    name,
    levelLabel(entry.level),
    `bought ${formatCalendarDate(entry.purchasedOn, context.today)}`,
    location,
    expiryLabel(entry, context.today).text.toLowerCase(),
    change,
  ]
    .filter(Boolean)
    .join(', ');
}

function highlightStyle(styles: ReturnType<typeof createStyles>, tone: ExpiryTone | undefined) {
  if (tone === 'soon') return styles.cardSoon;
  if (tone === 'expired') return styles.cardExpired;
  return null;
}

function createStyles(theme: Theme) {
  return {
    list: { flex: 1 },
    content: { flexGrow: 1 },
    separator: { height: theme.spacing.sm },
    sectionHeader: {
      flexDirection: 'row' as const,
      justifyContent: 'space-between' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.sm,
      paddingHorizontal: theme.spacing.xs,
      paddingBottom: theme.spacing.sm,
    },
    sectionHeaderSpaced: { paddingTop: theme.spacing.xl },
    sectionTitle: {
      ...theme.typography.overline,
      flexShrink: 1,
      color: theme.colors.mutedText,
    },
    sectionCount: {
      ...theme.typography.overline,
      color: theme.colors.mutedText,
    },
    card: {
      backgroundColor: theme.colors.surface,
      borderColor: theme.colors.border,
      borderWidth: 1,
      borderRadius: theme.radii.card,
      overflow: 'hidden' as const,
    },
    cardSoon: { borderColor: theme.colors.expiringSoon },
    cardExpired: { borderColor: theme.colors.danger },
    row: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.md,
      minHeight: MIN_TOUCH_TARGET,
      paddingVertical: theme.spacing.md,
      paddingHorizontal: theme.spacing.lg,
    },
    entryRow: {
      paddingLeft: theme.spacing.xxl,
      borderTopWidth: 1,
      borderTopColor: theme.colors.border,
      backgroundColor: theme.colors.background,
    },
    rowText: {
      flex: 1,
      minWidth: 0,
      gap: theme.spacing.xs,
    },
    nameLine: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.sm,
    },
    name: {
      ...theme.typography.body,
      fontFamily: theme.typography.label.fontFamily,
      fontWeight: theme.typography.label.fontWeight,
      color: theme.colors.text,
    },
    // Shrinks (and wraps, then truncates) so a long name never pushes the count off the row.
    groupName: { flexShrink: 1 },
    countBadge: {
      flexShrink: 0,
      paddingHorizontal: 7,
      paddingVertical: 1,
      borderRadius: theme.radii.pill,
      backgroundColor: theme.colors.primarySubtle,
    },
    countText: {
      ...theme.typography.caption,
      fontFamily: theme.typography.label.fontFamily,
      fontWeight: theme.typography.label.fontWeight,
      color: theme.colors.text,
    },
    detailLine: {
      flexDirection: 'row' as const,
      flexWrap: 'wrap' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.sm,
    },
    caption: { ...theme.typography.caption, color: theme.colors.mutedText },
    bodyText: { color: theme.colors.text },
    mutedText: { color: theme.colors.mutedText },
  };
}

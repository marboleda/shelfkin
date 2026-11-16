// The Pantry list: one row per item ("Milk ×2"), in the API's order. A row
// with one entry shows that entry and opens it; a row with several expands to
// show each entry, and while collapsed summarises them.
//
// The list is a FlatList of groups, so only the rows near the screen are
// rendered, however large the pantry.

import Feather from '@expo/vector-icons/Feather';
import { memo, useCallback, useMemo, useState, type ReactElement } from 'react';
import { FlatList, Platform, Pressable, Text, View } from 'react-native';

import {
  expiryLabel,
  formatCalendarDate,
  groupAccessibilityLabel,
  lastChangeLabel,
  levelLabel,
  summarizeGroup,
  type ExpiryTone,
} from '@/pantry/format';
import type { Pantry, PantryGroup, PantryListEntry } from '@/pantry/pantry';
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

type PantryListProps = Omit<PantryRowContext, 'today'> & {
  pantry: Pantry;
  /** Rendered above the rows, scrolling with them. */
  header?: ReactElement | null;
  /** Space below the last row, e.g. so a floating button never covers it. */
  bottomInset?: number;
};

export function PantryList({ pantry, now, currentUserId, locationName, header, bottomInset = 0 }: PantryListProps) {
  const styles = useThemedStyles(createStyles);
  // Expanded rows, by catalog item ID. Kept across refetches (the IDs stay the
  // same); Pantry remounts the list when the household changes, which resets it.
  const [expanded, setExpanded] = useState<ReadonlySet<string>>(() => new Set());
  const toggle = useCallback((catalogItemId: string) => {
    setExpanded((previous) => {
      const next = new Set(previous);
      if (next.has(catalogItemId)) next.delete(catalogItemId);
      else next.add(catalogItemId);
      return next;
    });
  }, []);

  const context = useMemo<PantryRowContext>(
    () => ({ today: pantry.today, now, currentUserId, locationName }),
    [pantry.today, now, currentUserId, locationName],
  );

  return (
    <FlatList
      testID="pantry-list"
      style={styles.list}
      contentContainerStyle={[styles.content, { paddingBottom: bottomInset }]}
      data={pantry.groups}
      keyExtractor={(group) => group.catalogItemId}
      extraData={{ expanded, context }}
      renderItem={({ item }) => (
        <PantryGroupRow
          group={item}
          expanded={expanded.has(item.catalogItemId)}
          onToggle={toggle}
          context={context}
        />
      )}
      ListHeaderComponent={header ?? undefined}
      ItemSeparatorComponent={Separator}
      initialNumToRender={12}
      windowSize={7}
      // Detaches off-screen rows from the native view tree, which keeps long lists smooth on Android.
      removeClippedSubviews={Platform.OS === 'android'}
    />
  );
}

function Separator() {
  const styles = useThemedStyles(createStyles);
  return <View style={styles.separator} />;
}

type PantryGroupRowProps = {
  group: PantryGroup;
  expanded: boolean;
  onToggle: (catalogItemId: string) => void;
  context: PantryRowContext;
};

/** One item: its single entry, or a collapsible row of its entries. */
export const PantryGroupRow = memo(function PantryGroupRow({ group, expanded, onToggle, context }: PantryGroupRowProps) {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const entries = group.entries;

  if (entries.length === 1) {
    const entry = entries[0];
    const expiry = expiryLabel(entry, context.today);
    return (
      <View testID={`pantry-row-${group.catalogItemId}`} style={[styles.card, highlightStyle(styles, expiry.tone)]}>
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
    <View testID={`pantry-row-${group.catalogItemId}`} style={[styles.card, highlightStyle(styles, tone)]}>
      <Pressable
        onPress={() => onToggle(group.catalogItemId)}
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

// Display formatting for pantry entries: calendar dates, stock levels, expiry
// labels, relative times and "last change" labels. Pure functions only, so
// Pantry, Entry detail and the offline cache all word things the same way.
//
// Two kinds of date reach the app:
// - `YYYY-MM-DD` calendar dates (`purchasedOn`, `expiresOn`, the pantry's
//   `today`). They name a day, not an instant, so they are never turned into a
//   local `Date`: `new Date('2026-10-20')` is UTC midnight, which is Oct 19
//   west of UTC. They are formatted as UTC dates instead, which always shows the
//   same day.
// - RFC 3339 timestamps (`occurredAt`). These are instants, shown in the
//   device's time zone.
//
// Expiry status is never computed here: it comes from the API (`expiryStatus`,
// computed against the pantry's `today`), so the app always agrees with the
// expiry push notifications.

import { getLocales } from 'expo-localization';

import type { components } from '@/api/schema';

export type StockLevel = components['schemas']['StockLevel'];
export type ExpiryStatus = components['schemas']['ExpiryStatus'];
export type LastChange = NonNullable<components['schemas']['LastChange']>;

/** The fields of a pantry entry that its expiry label depends on. */
export type ExpiryFields = {
  level: StockLevel;
  expiresOn: string | null;
  expiryStatus: ExpiryStatus;
};

const DAY_MS = 24 * 60 * 60 * 1000;

/** The device's preferred locale (for month names), or the runtime default. */
export function deviceLocale(): string | undefined {
  try {
    return getLocales()[0]?.languageTag ?? undefined;
  } catch {
    return undefined;
  }
}

type CalendarDate = { year: number; month: number; day: number };

/** Parses a `YYYY-MM-DD` date, or returns null when it is not one. */
export function parseCalendarDate(value: string): CalendarDate | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return null;
  return { year: Number(match[1]), month: Number(match[2]), day: Number(match[3]) };
}

/** Milliseconds of UTC midnight on the calendar date, for arithmetic only. */
function utcMidnight(date: CalendarDate): number {
  return Date.UTC(date.year, date.month - 1, date.day);
}

/** Whole days from `from` to `to` (both `YYYY-MM-DD`): negative when `to` is earlier. */
export function daysBetween(from: string, to: string): number {
  const a = parseCalendarDate(from);
  const b = parseCalendarDate(to);
  if (!a || !b) return 0;
  return Math.round((utcMidnight(b) - utcMidnight(a)) / DAY_MS);
}

/**
 * A `YYYY-MM-DD` date as "Oct 20", or "Oct 20, 2025" when its year is not the
 * year of `today` (also `YYYY-MM-DD`). Never shifted by the device's time zone.
 */
export function formatCalendarDate(value: string, today: string, locale = deviceLocale()): string {
  const date = parseCalendarDate(value);
  if (!date) return value;
  const sameYear = parseCalendarDate(today)?.year === date.year;
  return new Intl.DateTimeFormat(locale, {
    month: 'short',
    day: 'numeric',
    ...(sameYear ? {} : { year: 'numeric' }),
    timeZone: 'UTC',
  }).format(new Date(utcMidnight(date)));
}

/** A timestamp's day in the device's time zone, as "Oct 20" (or "Oct 20, 2025" in another year than `now`). */
export function formatTimestampDate(value: string | Date, now: Date, locale = deviceLocale()): string {
  const date = typeof value === 'string' ? new Date(value) : value;
  const sameYear = date.getFullYear() === now.getFullYear();
  return new Intl.DateTimeFormat(locale, {
    month: 'short',
    day: 'numeric',
    ...(sameYear ? {} : { year: 'numeric' }),
  }).format(date);
}

const LEVEL_LABELS: Record<StockLevel, string> = {
  full: 'Full',
  half: 'Half',
  low: 'Low',
  out: 'Out',
};

/** "Full", "Half", "Low" or "Out". */
export function levelLabel(level: StockLevel): string {
  return LEVEL_LABELS[level] ?? level;
}

/** How an expiry label is shown: `soon` and `expired` are highlighted, `muted` is for out entries. */
export type ExpiryTone = 'soon' | 'expired' | 'plain' | 'muted';

export type ExpiryLabel = { text: string; tone: ExpiryTone };

/**
 * The expiry label of an entry, from the API's `expiryStatus` and `today`:
 * "Expires today", "Expires tomorrow", "Expires in 3 days", "Expired Oct 25",
 * "Expires Oct 30" or "No expiry". Out entries are never highlighted (the API
 * gives them `expiryStatus: null`).
 */
export function expiryLabel(entry: ExpiryFields, today: string, locale = deviceLocale()): ExpiryLabel {
  const muted = entry.level === 'out';
  if (!entry.expiresOn) return { text: 'No expiry', tone: muted ? 'muted' : 'plain' };
  const date = formatCalendarDate(entry.expiresOn, today, locale);
  if (!muted && entry.expiryStatus === 'expired') return { text: `Expired ${date}`, tone: 'expired' };
  if (!muted && entry.expiryStatus === 'soon') {
    const days = Math.max(0, daysBetween(today, entry.expiresOn));
    const text = days === 0 ? 'Expires today' : days === 1 ? 'Expires tomorrow' : `Expires in ${days} days`;
    return { text, tone: 'soon' };
  }
  return { text: `Expires ${date}`, tone: muted ? 'muted' : 'plain' };
}

/**
 * How long ago `occurredAt` was: "just now" (under a minute), "5m ago",
 * "2h ago", "yesterday" (24 to 48 hours), "3d ago", and from 7 days the date
 * ("Oct 20"). A time in the future (a device clock behind the server's) is "just now".
 */
export function relativeTime(occurredAt: string | Date, now: Date, locale = deviceLocale()): string {
  const then = typeof occurredAt === 'string' ? new Date(occurredAt) : occurredAt;
  const elapsed = now.getTime() - then.getTime();
  if (Number.isNaN(elapsed)) return '';
  const minutes = Math.floor(elapsed / 60_000);
  if (minutes < 1) return 'just now';
  if (minutes < 60) return `${minutes}m ago`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h ago`;
  if (hours < 48) return 'yesterday';
  const days = Math.floor(hours / 24);
  if (days < 7) return `${days}d ago`;
  return formatTimestampDate(then, now, locale);
}

export type LastChangeContext = {
  /** The signed-in user's ID, so their own changes say "by you". */
  currentUserId?: string | null;
  /** A location's name by ID, for "Moved to Freezer". */
  locationName?: (id: number) => string | undefined;
  now: Date;
  locale?: string;
};

/** What changed, without the actor or time: "Set to Low", "Moved to Freezer", "Added". */
function changeAction(change: LastChange, locationName?: (id: number) => string | undefined): string {
  const changes = change.changes ?? {};
  switch (change.type) {
    case 'entry_created':
      return 'Added';
    case 'level_changed': {
      const to = changes.level?.to;
      return typeof to === 'string' && to in LEVEL_LABELS ? `Set to ${levelLabel(to as StockLevel)}` : 'Level changed';
    }
    case 'location_changed': {
      const to = changes.location_id?.to;
      const name = typeof to === 'number' ? locationName?.(to) : undefined;
      return name ? `Moved to ${name}` : 'Moved';
    }
    case 'expiry_changed':
      return changes.expires_on?.to === null ? 'Expiry removed' : 'Expiry changed';
    case 'purchase_date_changed':
      return 'Purchase date changed';
    case 'entry_deleted':
      return 'Removed';
    default:
      return 'Changed';
  }
}

/**
 * The label for an entry's latest change, e.g. "Set to Low by Sam, 2h ago",
 * "Added by you, just now" or (for a deleted account) "Set to Low, 2h ago".
 * Null when the entry has no recorded change.
 */
export function lastChangeLabel(change: LastChange | null, context: LastChangeContext): string | null {
  if (!change) return null;
  const action = changeAction(change, context.locationName);
  let actor = '';
  if (change.actorId && context.currentUserId && change.actorId === context.currentUserId) {
    actor = ' by you';
  } else if (change.actorName) {
    actor = ` by ${change.actorName}`;
  }
  const when = relativeTime(change.occurredAt, context.now, context.locale);
  return when ? `${action}${actor}, ${when}` : `${action}${actor}`;
}

/** The fields of a pantry entry that a group summary depends on. */
export type SummaryEntry = ExpiryFields;

export type GroupSummary = {
  /** Each entry's level, in entry order: "Full, Low". */
  levels: string;
  /** The most urgent expiry among the entries that are not out, or null when none has one. */
  expiry: ExpiryLabel | null;
  /** Whether any entry is `soon` or `expired`, so the row is highlighted. */
  highlighted: boolean;
  /** Whether every entry is out, so the row is muted. */
  allOut: boolean;
};

/** Summarises several entries of one item for its collapsed row. */
export function summarizeGroup(entries: readonly SummaryEntry[], today: string, locale = deviceLocale()): GroupSummary {
  const levels = entries.map((entry) => levelLabel(entry.level)).join(', ');
  const allOut = entries.length > 0 && entries.every((entry) => entry.level === 'out');
  const highlighted = entries.some(
    (entry) => entry.level !== 'out' && (entry.expiryStatus === 'soon' || entry.expiryStatus === 'expired'),
  );
  // `YYYY-MM-DD` strings sort as dates, and an earlier date is always more urgent
  // (expired before soon before later).
  let mostUrgent: SummaryEntry | null = null;
  for (const entry of entries) {
    if (entry.level === 'out' || !entry.expiresOn) continue;
    if (!mostUrgent || entry.expiresOn < (mostUrgent.expiresOn as string)) mostUrgent = entry;
  }
  return { levels, expiry: mostUrgent ? expiryLabel(mostUrgent, today, locale) : null, highlighted, allOut };
}

function lowerFirst(text: string): string {
  return text.charAt(0).toLowerCase() + text.slice(1);
}

/**
 * What a screen reader says for a collapsed row with several entries, e.g.
 * "Milk, 2 entries, Full, Low, one expires tomorrow".
 */
export function groupAccessibilityLabel(
  name: string,
  entries: readonly SummaryEntry[],
  today: string,
  locale = deviceLocale(),
): string {
  const parts = [name, `${entries.length} entries`];
  const summary = summarizeGroup(entries, today, locale);
  parts.push(summary.allOut ? 'all out' : summary.levels);
  const urgent = entries.filter(
    (entry) => entry.level !== 'out' && (entry.expiryStatus === 'soon' || entry.expiryStatus === 'expired'),
  );
  if (urgent.length === 1) {
    parts.push(`one ${lowerFirst(expiryLabel(urgent[0], today, locale).text)}`);
  } else if (urgent.length > 1 && summary.expiry) {
    parts.push(`${urgent.length} expiring soon or expired, the first ${lowerFirst(summary.expiry.text)}`);
  }
  return parts.join(', ');
}

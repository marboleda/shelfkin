/**
 * Starter design tokens for Shelfkin.
 *
 * This is the single place where colour, spacing, type and radius values live.
 * Screens and components read them through `useTheme()` / `useThemedStyles()`,
 * so the designed token set (#44) can replace these values without touching screens.
 */

export type ColorSchemeName = 'light' | 'dark';

export type ColorTokens = {
  /** App and screen background. */
  background: string;
  /** Raised surfaces: cards, rows, tab bar, side nav. */
  surface: string;
  /** Primary body text. */
  text: string;
  /** Secondary text (captions, hints, inactive nav labels). */
  mutedText: string;
  /** Hairlines and outlines. */
  border: string;
  /** Brand / primary action colour. */
  primary: string;
  /** Text and icons drawn on top of `primary`. */
  onPrimary: string;
  /** Tinted background for the selected navigation item. */
  primarySubtle: string;
  /** Destructive actions and errors. */
  danger: string;
  /** Highlight for entries that are expiring soon (see docs/data-model.md). */
  expiringSoon: string;
};

export const colors: Record<ColorSchemeName, ColorTokens> = {
  light: {
    background: '#F7F6F2',
    surface: '#FFFFFF',
    text: '#1C1B19',
    mutedText: '#5F5C55',
    border: '#DDD9CF',
    primary: '#2F6B4F',
    onPrimary: '#FFFFFF',
    primarySubtle: '#E3EFE8',
    danger: '#B3261E',
    expiringSoon: '#A35200',
  },
  dark: {
    background: '#141513',
    surface: '#1E201D',
    text: '#ECEAE4',
    mutedText: '#A6A39B',
    border: '#363830',
    primary: '#7BC5A0',
    onPrimary: '#0F2A1D',
    primarySubtle: '#23382C',
    danger: '#F2B8B5',
    expiringSoon: '#F5B74A',
  },
};

/** Spacing scale in density-independent pixels. Same in both modes. */
export const spacing = {
  xs: 4,
  sm: 8,
  md: 12,
  lg: 16,
  xl: 24,
  xxl: 32,
} as const;

/** Type scale. Same in both modes. */
export const typography = {
  caption: { fontSize: 13, lineHeight: 18, fontWeight: '400' },
  body: { fontSize: 16, lineHeight: 24, fontWeight: '400' },
  label: { fontSize: 15, lineHeight: 20, fontWeight: '600' },
  title: { fontSize: 20, lineHeight: 26, fontWeight: '600' },
  heading: { fontSize: 28, lineHeight: 34, fontWeight: '700' },
} as const;

/** Corner radii. Same in both modes. */
export const radii = {
  sm: 6,
  md: 10,
  lg: 16,
  pill: 999,
} as const;

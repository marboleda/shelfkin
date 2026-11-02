/**
 * Design tokens for Shelfkin, from the screen designs recorded in docs/designs.md.
 *
 * This is the single place where colour, spacing, type and radius values live.
 * Screens and components read them through `useTheme()` / `useThemedStyles()`.
 */
import { Platform } from 'react-native';

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
  /** Destructive actions, errors and "Expired" text. */
  danger: string;
  /** Background of the "Expired" chip, behind `danger` text. */
  dangerSubtle: string;
  /** "Expires soon" text for entries that are expiring soon (see docs/data-model.md). */
  expiringSoon: string;
  /** Background of the "Expires soon" chip, behind `expiringSoon` text. */
  expiringSubtle: string;
  /** Filled bars of the level meter at Low. A graphic only: never use it for text. */
  levelLow: string;
  /** Unfilled bars of the level meter. A graphic only: never use it for text. */
  levelEmpty: string;
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
    dangerSubtle: '#F9E1DE',
    expiringSoon: '#8A4B00',
    expiringSubtle: '#FBEBD9',
    levelLow: '#C27A12',
    levelEmpty: '#E4E0D6',
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
    dangerSubtle: '#3A1F1D',
    expiringSoon: '#F5B74A',
    expiringSubtle: '#3A2A14',
    levelLow: '#E0A04A',
    levelEmpty: '#3A3C35',
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

/**
 * The Figtree faces the type scale uses, by weight. Each is loaded under its own family
 * name (see `fonts.ts`), because on Android a custom font does not pick a face from
 * `fontWeight`: each type style must name the face for its weight.
 */
export const fontFaces = {
  '400': 'Figtree_400Regular',
  '600': 'Figtree_600SemiBold',
  '700': 'Figtree_700Bold',
} as const;

export type FontWeightToken = keyof typeof fontFaces;

/**
 * Used on web when Figtree is not available (for example offline with nothing cached).
 * Native platforms fall back to the system font for an unknown family on their own.
 */
const WEB_FALLBACK_FONTS = "system-ui, -apple-system, 'Segoe UI', Roboto, 'Helvetica Neue', Arial, sans-serif";

function fontFamilyFor(weight: FontWeightToken): string {
  return Platform.OS === 'web' ? `${fontFaces[weight]}, ${WEB_FALLBACK_FONTS}` : fontFaces[weight];
}

/**
 * Type scale, in Figtree. Same in both modes. `fontWeight` matches the named face, so the
 * system-font fallback keeps the same weight if Figtree fails to load.
 */
export const typography = {
  caption: { fontFamily: fontFamilyFor('400'), fontSize: 13, lineHeight: 18, fontWeight: '400' },
  body: { fontFamily: fontFamilyFor('400'), fontSize: 16, lineHeight: 24, fontWeight: '400' },
  label: { fontFamily: fontFamilyFor('600'), fontSize: 15, lineHeight: 20, fontWeight: '600' },
  title: { fontFamily: fontFamilyFor('700'), fontSize: 20, lineHeight: 26, fontWeight: '700' },
  heading: { fontFamily: fontFamilyFor('700'), fontSize: 28, lineHeight: 34, fontWeight: '700' },
  /** Section headers such as "FRIDGE". */
  overline: {
    fontFamily: fontFamilyFor('700'),
    fontSize: 13,
    lineHeight: 18,
    fontWeight: '700',
    textTransform: 'uppercase',
    letterSpacing: 0.6,
  },
} as const;

/** Corner radii. Same in both modes. */
export const radii = {
  sm: 6,
  md: 10,
  /** Cards and grouped rows. */
  card: 12,
  lg: 16,
  pill: 999,
} as const;

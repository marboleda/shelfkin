import { useMemo } from 'react';
import { StyleSheet, useColorScheme, type ImageStyle, type TextStyle, type ViewStyle } from 'react-native';
import { DarkTheme, DefaultTheme } from 'expo-router';

import { colors, radii, spacing, typography, type ColorSchemeName, type ColorTokens } from './tokens';

export * from './tokens';
export * from './layout';

export type Theme = {
  scheme: ColorSchemeName;
  dark: boolean;
  colors: ColorTokens;
  spacing: typeof spacing;
  typography: typeof typography;
  radii: typeof radii;
};

export function getTheme(scheme: ColorSchemeName | null | undefined): Theme {
  const resolved: ColorSchemeName = scheme === 'dark' ? 'dark' : 'light';
  return {
    scheme: resolved,
    dark: resolved === 'dark',
    colors: colors[resolved],
    spacing,
    typography,
    radii,
  };
}

/** The current theme. Follows the device's light/dark setting and updates live. */
export function useTheme(): Theme {
  const scheme = useColorScheme();
  return useMemo(() => getTheme(scheme === 'dark' ? 'dark' : 'light'), [scheme]);
}

type NamedStyles = Record<string, ViewStyle | TextStyle | ImageStyle>;

/** Builds a `StyleSheet` from the current theme, rebuilt only when the theme changes. */
export function useThemedStyles<T extends NamedStyles>(factory: (theme: Theme) => T): Readonly<T> {
  const theme = useTheme();
  // Pass a module-level factory so it is stable and styles are only rebuilt on theme change.
  return useMemo(() => StyleSheet.create(factory(theme)), [factory, theme]);
}

/** React Navigation theme derived from our tokens, so navigator backgrounds never flash white. */
export function getNavigationTheme(theme: Theme) {
  const base = theme.dark ? DarkTheme : DefaultTheme;
  return {
    ...base,
    dark: theme.dark,
    colors: {
      ...base.colors,
      primary: theme.colors.primary,
      background: theme.colors.background,
      card: theme.colors.surface,
      text: theme.colors.text,
      border: theme.colors.border,
      notification: theme.colors.danger,
    },
  };
}

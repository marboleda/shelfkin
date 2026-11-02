import Feather from '@expo/vector-icons/Feather';
import { Figtree_400Regular } from '@expo-google-fonts/figtree/400Regular';
import { Figtree_600SemiBold } from '@expo-google-fonts/figtree/600SemiBold';
import { Figtree_700Bold } from '@expo-google-fonts/figtree/700Bold';
import { useFonts, type FontSource } from 'expo-font';
import { useEffect, useState } from 'react';

import { fontFaces } from './tokens';

/**
 * The fonts the app loads before it first renders: the three Figtree faces the type scale
 * uses (and no others), and the Feather icon font used by the nav.
 *
 * Each Figtree face gets its own family name so native platforms can select it by name.
 * `weight` is only used on web, where it becomes the `@font-face` weight so the browser
 * does not draw a synthetic bold on top of an already bold face.
 */
export const fontSources: Record<string, FontSource> = {
  [fontFaces['400']]: { uri: Figtree_400Regular, weight: 400 },
  [fontFaces['600']]: { uri: Figtree_600SemiBold, weight: 600 },
  [fontFaces['700']]: { uri: Figtree_700Bold, weight: 700 },
  ...Feather.font,
};

/**
 * How long the splash screen waits for fonts before the app opens anyway. If the fonts
 * never settle (for example a request that hangs offline), text renders in the system font.
 */
export const FONT_LOAD_TIMEOUT_MS = 4000;

/**
 * Loads the app fonts. Returns `true` once the app can render: when the fonts have loaded,
 * failed to load, or taken longer than `FONT_LOAD_TIMEOUT_MS`.
 */
export function useAppFonts(): boolean {
  const [loaded, error] = useFonts(fontSources);
  const [timedOut, setTimedOut] = useState(false);
  const settled = loaded || error != null;

  useEffect(() => {
    if (settled) return;
    const timer = setTimeout(() => setTimedOut(true), FONT_LOAD_TIMEOUT_MS);
    return () => clearTimeout(timer);
  }, [settled]);

  useEffect(() => {
    if (error) {
      console.warn('Fonts failed to load; using the system font.', error);
    }
  }, [error]);

  return settled || timedOut;
}

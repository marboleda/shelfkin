import { Platform } from 'react-native';

/**
 * Whether Add item's suggestions are driven by a keyboard: on web (and desktop
 * browsers) the first suggestion is highlighted, Up and Down move the highlight
 * and Enter picks it. On phones the user taps a suggestion instead.
 */
export function usesKeyboardHighlight(): boolean {
  return Platform.OS === 'web';
}

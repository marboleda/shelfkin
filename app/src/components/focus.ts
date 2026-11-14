import { Platform } from 'react-native';

/**
 * Moves keyboard focus to an element (web, and hardware keyboards). Elsewhere
 * `focus()` does nothing, and screen readers move into and out of a modal on
 * their own. Accepts anything a ref holds, including null.
 */
export function focusElement(element: unknown) {
  (element as { focus?: () => void } | null)?.focus?.();
}

/**
 * Whether focus can go back to the element that opened a Modal only once the
 * Modal's `onDismiss` has run. True on web: RN-Web's Modal traps focus while it
 * is mounted (focusing anything outside it is undone), and `onDismiss` runs
 * after it has unmounted.
 */
export function focusWaitsForModalDismiss(): boolean {
  return Platform.OS === 'web';
}

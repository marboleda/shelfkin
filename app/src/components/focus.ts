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
 * Whether keyboard focus is still where a screen left it: on nothing (the page
 * body) or on `element`. False once the user has clicked or tabbed anywhere
 * else, so a deferred `focusElement` would take focus away from them. Always
 * true off the web, where `focusElement` does nothing.
 */
export function focusIsOnBodyOr(element: unknown): boolean {
  if (Platform.OS !== 'web' || typeof document === 'undefined') return true;
  const active = document.activeElement;
  return active === null || active === document.body || (element != null && active === element);
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

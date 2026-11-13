/**
 * Moves keyboard focus to an element (web, and hardware keyboards). Elsewhere
 * `focus()` does nothing, and screen readers move into and out of a modal on
 * their own. Accepts anything a ref holds, including null.
 */
export function focusElement(element: unknown) {
  (element as { focus?: () => void } | null)?.focus?.();
}

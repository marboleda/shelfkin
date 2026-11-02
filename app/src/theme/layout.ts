/**
 * Window width (in px / dp) at which the app switches from the phone layout
 * (bottom tab bar) to the desktop layout (left side nav). Change it here only.
 */
export const WIDE_LAYOUT_BREAKPOINT = 768;

/** Maximum width of screen content, so it stays readable on wide windows. */
export const CONTENT_MAX_WIDTH = 720;

/** Width of the left side nav in the wide layout. */
export const SIDE_NAV_WIDTH = 220;

/** Smallest size, in px / dp, of anything the user taps (WCAG / platform guidance). */
export const MIN_TOUCH_TARGET = 44;

export function isWideLayout(windowWidth: number): boolean {
  return windowWidth >= WIDE_LAYOUT_BREAKPOINT;
}

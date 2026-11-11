import type { Href } from 'expo-router';

/** Search parameter on /sign-in and /sign-up holding the route to open after signing in. */
export const REDIRECT_PARAM = 'redirect';

const GUEST_PATHS = ['/sign-in', '/sign-up'];

function isGuestPath(path: string): boolean {
  const pathOnly = path.split(/[?#]/)[0].replace(/\/+$/, '');
  return GUEST_PATHS.includes(pathOnly);
}

/**
 * The route to open after signing in. Only paths inside the app are accepted
 * (no `//host` or `https://` addresses), so a crafted link cannot send the
 * user to another site. Anything else, or nothing, goes to Pantry.
 */
export function redirectTarget(param: string | string[] | undefined): Href {
  const value = Array.isArray(param) ? param[0] : param;
  if (!value || !value.startsWith('/') || value.startsWith('//') || value.includes('\\') || isGuestPath(value)) {
    return '/';
  }
  return value as Href;
}

/** /sign-in, remembering `requestedPath` to return to unless it is Pantry. */
export function signInHref(requestedPath: string | null | undefined): Href {
  if (!requestedPath || requestedPath === '/' || isGuestPath(requestedPath)) return '/sign-in';
  return { pathname: '/sign-in', params: { [REDIRECT_PARAM]: requestedPath } };
}

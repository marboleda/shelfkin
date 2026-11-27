import { getIdToken, onSignedOut } from '@/auth/session';

/** Extra headers to send with an API request. */
export type RequestHeaders = Record<string, string>;

export type RequestHeaderOptions = {
  /** Get a new Firebase ID token instead of the cached one (used to retry after a 401). */
  forceRefresh?: boolean;
};

/**
 * The one place the app adds headers to API requests. The client calls it
 * before every request and sets each returned header.
 *
 * While someone is signed in it adds `Authorization: Bearer <Firebase ID token>`.
 * Firebase caches the token and refreshes it before it expires, so an app left
 * open for hours still sends a valid one.
 */
export async function getRequestHeaders(options: RequestHeaderOptions = {}): Promise<RequestHeaders> {
  const token = await getIdToken(options.forceRefresh ?? false);
  lastHeaders = token ? { Authorization: `Bearer ${token}` } : {};
  return lastHeaders;
}

let lastHeaders: RequestHeaders = {};
onSignedOut(() => {
  lastHeaders = {};
});

/**
 * The headers `getRequestHeaders` returned last, without waiting for anything.
 * For a request that must leave before the page unloads (see
 * `sendWithKeepalive` in client.ts), where waiting for Firebase could be too
 * late. The token may have expired if no request was made for an hour; the API
 * then answers 401.
 */
export function cachedRequestHeaders(): RequestHeaders {
  return lastHeaders;
}

// Where the app finds the API. See app/README.md ("API URL") for how to set it.

/** The local API (the Go server listens on port 8080), used in development when no URL is set. */
export const LOCAL_API_URL = 'http://localhost:8080';

export type ApiConfig =
  | { ok: true; baseUrl: string }
  | { ok: false; message: string };

/** Drops trailing slashes so `https://x/` and `https://x` build the same request URLs. */
export function normalizeBaseUrl(url: string): string {
  return url.trim().replace(/\/+$/, '');
}

/**
 * Picks the API base URL from `EXPO_PUBLIC_API_URL`.
 *
 * - Set: that URL, without trailing slashes.
 * - Unset in development: the local API.
 * - Unset in a production build: a configuration error, so the app never
 *   silently calls the wrong server.
 */
export function resolveApiConfig(envUrl: string | undefined, isDev: boolean): ApiConfig {
  const url = envUrl ? normalizeBaseUrl(envUrl) : '';
  if (url) {
    return { ok: true, baseUrl: url };
  }
  if (isDev) {
    return { ok: true, baseUrl: LOCAL_API_URL };
  }
  return {
    ok: false,
    message:
      'This build has no server address. Set EXPO_PUBLIC_API_URL when building the app (see app/.env.example).',
  };
}

// `process.env.EXPO_PUBLIC_API_URL` must be written out in full (dot notation)
// so Expo can inline it at build time.
export const apiConfig: ApiConfig = resolveApiConfig(process.env.EXPO_PUBLIC_API_URL, __DEV__);

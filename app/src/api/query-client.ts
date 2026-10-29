import { QueryClient, type DefaultOptions } from '@tanstack/react-query';

import { isRetryableError } from './errors';

/** Failed queries are tried at most this many more times (network errors and 5xx only). */
export const MAX_QUERY_RETRIES = 2;

/** Retry network errors and 5xx at most twice; never retry 4xx or unexpected errors without a 5xx status. */
export function shouldRetryQuery(failureCount: number, error: unknown): boolean {
  return failureCount < MAX_QUERY_RETRIES && isRetryableError(error);
}

/**
 * Builds the app's `QueryClient`. The root layout creates one and shares it
 * with the whole app; tests create a fresh one each so cached results never
 * leak between them.
 */
export function createQueryClient(overrides: DefaultOptions['queries'] = {}): QueryClient {
  return new QueryClient({
    defaultOptions: {
      queries: {
        retry: shouldRetryQuery,
        // Refetching on focus and app foreground is decided in #36.
        refetchOnWindowFocus: false,
        ...overrides,
      },
    },
  });
}

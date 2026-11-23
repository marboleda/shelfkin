import type { QueryClient } from '@tanstack/react-query';

import { shouldRetryQuery } from '@/api/query-client';

/** How long to wait before retry number `failureCount` (1 for the first retry). */
export type RetryDelay = (failureCount: number, error: unknown) => number;

/** TanStack Query's default backoff: 1 s, 2 s, 4 s, ... up to 30 s. */
export const defaultRetryDelay: RetryDelay = (failureCount) => Math.min(1000 * 2 ** (failureCount - 1), 30_000);

/**
 * The retry delay the app's queries use (tests set it to 0), so a write retries
 * on the same schedule as a query.
 */
export function queryRetryDelay(queryClient: QueryClient): RetryDelay {
  const configured = queryClient.getDefaultOptions().queries?.retryDelay;
  if (typeof configured === 'number') return () => configured;
  if (typeof configured === 'function') {
    return (failureCount, error) => configured(failureCount, error as Error);
  }
  return defaultRetryDelay;
}

/**
 * Runs `send`, retrying a network error or 5xx with the same limit as queries
 * (`MAX_QUERY_RETRIES`). A 4xx is thrown at once. Only for requests that are
 * safe to repeat, such as a create with client-generated IDs.
 */
export async function withRetries<T>(send: () => Promise<T>, delay: RetryDelay = defaultRetryDelay): Promise<T> {
  let failures = 0;
  for (;;) {
    try {
      return await send();
    } catch (error) {
      if (!shouldRetryQuery(failures, error)) throw error;
      failures += 1;
      const wait = delay(failures, error);
      if (wait > 0) await new Promise((resolve) => setTimeout(resolve, wait));
    }
  }
}

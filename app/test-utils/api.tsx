// Helpers for tests that call the API. MSW answers the requests; nothing
// reaches a real server, and an unhandled request fails the test.

import { QueryClientProvider, type QueryClient } from '@tanstack/react-query';
import type { RequestHandler } from 'msw';
import { setupServer } from 'msw/node';
import type { ReactNode } from 'react';

import { LOCAL_API_URL } from '@/api/config';
import { createQueryClient } from '@/api/query-client';

/** In tests `EXPO_PUBLIC_API_URL` is unset and `__DEV__` is true, so the app calls the local API. */
export const API_URL = LOCAL_API_URL;

export function apiUrl(path: string): string {
  return `${API_URL}${path}`;
}

/**
 * Starts an MSW server for the test file and resets its handlers after each
 * test. `handlers` stay in place for every test (see `signedInApiHandlers` in
 * test-utils/auth.ts); `server.use()` adds more for one test.
 */
export function setupApiServer(...handlers: RequestHandler[]) {
  const server = setupServer(...handlers);
  beforeAll(() => server.listen({ onUnhandledFrame: 'error' }));
  afterEach(() => server.resetHandlers());
  afterAll(() => server.close());
  return server;
}

/** A fresh `QueryClient` with the app's defaults, retrying without delay so tests stay fast. */
export function createTestQueryClient(): QueryClient {
  // gcTime Infinity schedules no garbage-collection timers, so Jest can exit cleanly.
  return createQueryClient({ retryDelay: 0, gcTime: Infinity });
}

export function queryWrapper(client: QueryClient) {
  return function QueryWrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
  };
}

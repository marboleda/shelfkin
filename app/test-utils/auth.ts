// Helpers for tests that need a signed-in user. Firebase Auth is replaced by
// the in-memory fake in jest/firebase-auth-mock.ts; jest/setup-auth.js signs
// out and clears it before every test.

import { http, HttpResponse } from 'msw';

import type { components } from '@/api/schema';

import { mockFirebase } from '../jest/firebase-auth-mock';
import { apiUrl } from './api';

export { mockFirebase };

type User = components['schemas']['User'];

export const TEST_EMAIL = 'sam@example.com';
export const TEST_PASSWORD = 'correct-horse';

/** The profile `GET /me` returns in tests unless a test says otherwise. */
export function testMe(overrides: Partial<User> = {}): User {
  return {
    id: '0190f3a1-7c2e-7b3d-9a4f-2c1e5d6b7a80',
    displayName: 'Sam',
    email: TEST_EMAIL,
    timezone: 'America/Toronto',
    pushEnabled: false,
    ...overrides,
  };
}

/** Signs the test user in, as if Firebase restored their session at start. */
export function signInTestUser(email = TEST_EMAIL, password = TEST_PASSWORD) {
  return mockFirebase.signIn(email, password);
}

/**
 * `GET /me`, `PATCH /me` (echoing the changes) and `GET /health`, for tests
 * that render the signed-in app but are not about the profile.
 */
export function signedInApiHandlers(me: User = testMe()) {
  let current = me;
  return [
    http.get(apiUrl('/me'), () => HttpResponse.json(current)),
    http.patch(apiUrl('/me'), async ({ request }) => {
      current = { ...current, ...((await request.json()) as Partial<User>) };
      return HttpResponse.json(current);
    }),
    http.get(apiUrl('/health'), () => HttpResponse.json({ status: 'ok' })),
  ];
}

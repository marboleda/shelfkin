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

type Household = components['schemas']['Household'];

export const TEST_HOUSEHOLD_ID = '0190f3a1-7c2e-7b3d-9a4f-000000000001';

/** A household `GET /households` returns in tests unless a test says otherwise. */
export function testHousehold(overrides: Partial<Household> = {}): Household {
  return {
    id: TEST_HOUSEHOLD_ID,
    name: 'Home',
    role: 'owner',
    createdAt: '2026-01-31T18:04:05Z',
    ...overrides,
  };
}

/** `GET /households` answering with `households` (by default one household, "Home"). */
export function householdsHandler(households: Household[] = [testHousehold()]) {
  return http.get(apiUrl('/households'), () => HttpResponse.json({ households }));
}

/**
 * `GET /me`, `PATCH /me` (echoing the changes), `GET /health` and
 * `GET /households` (one household, "Home"), for tests that render the
 * signed-in app but are not about the profile or households.
 */
export function signedInApiHandlers(me: User = testMe(), households: Household[] = [testHousehold()]) {
  let current = me;
  return [
    householdsHandler(households),
    http.get(apiUrl('/me'), () => HttpResponse.json(current)),
    http.patch(apiUrl('/me'), async ({ request }) => {
      current = { ...current, ...((await request.json()) as Partial<User>) };
      return HttpResponse.json(current);
    }),
    http.get(apiUrl('/health'), () => HttpResponse.json({ status: 'ok' })),
  ];
}

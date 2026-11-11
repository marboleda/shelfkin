import { waitFor } from '@testing-library/react-native';
import { http, HttpResponse } from 'msw';

import { apiClient, callApi } from '@/api/client';
import { ApiError } from '@/api/errors';
import { addBeforeSignOutTask, getAuthState } from '@/auth/session';
import { apiUrl, setupApiServer } from '../test-utils/api';
import { mockFirebase, signInTestUser, testMe } from '../test-utils/auth';

const server = setupApiServer();

function getMe() {
  return callApi(() => apiClient.GET('/me'));
}

/** Answers GET /me with the given statuses in turn, recording each Authorization header. */
function meResponses(statuses: number[]) {
  const seen: (string | null)[] = [];
  server.use(
    http.get(apiUrl('/me'), ({ request }) => {
      seen.push(request.headers.get('authorization'));
      const status = statuses[Math.min(seen.length - 1, statuses.length - 1)];
      return status === 200
        ? HttpResponse.json(testMe())
        : HttpResponse.json({ error: { code: 'unauthorized', message: 'A valid Firebase ID token is required.' } }, { status });
    }),
  );
  return seen;
}

test('every request carries the signed-in user\'s Firebase ID token', async () => {
  const user = signInTestUser();
  const seen = meResponses([200]);

  await getMe();

  expect(seen).toEqual([`Bearer token-${user.uid}-1`]);
  expect(user.getIdToken).toHaveBeenCalledWith(false);
});

test('signed out, requests have no Authorization header', async () => {
  const seen = meResponses([200]);

  await getMe();

  expect(seen).toEqual([null]);
});

test('a 401 forces a token refresh and retries the request once', async () => {
  const user = signInTestUser();
  const seen = meResponses([401, 200]);

  const me = await getMe();

  expect(me.displayName).toBe('Sam');
  expect(seen).toEqual([`Bearer token-${user.uid}-1`, `Bearer token-${user.uid}-2`]);
  expect(user.getIdToken).toHaveBeenLastCalledWith(true);
  expect(mockFirebase.currentUser).toBe(user);
});

test('a retried request keeps its body', async () => {
  signInTestUser();
  const bodies: unknown[] = [];
  server.use(
    http.patch(apiUrl('/me'), async ({ request }) => {
      bodies.push(await request.json());
      return bodies.length === 1
        ? HttpResponse.json({ error: { code: 'unauthorized', message: 'no' } }, { status: 401 })
        : HttpResponse.json(testMe({ displayName: 'Alex' }));
    }),
  );

  await callApi(() => apiClient.PATCH('/me', { body: { displayName: 'Alex' } }));

  expect(bodies).toEqual([{ displayName: 'Alex' }, { displayName: 'Alex' }]);
});

test('a second 401 signs the user out, runs the sign-out tasks, and says the session ended', async () => {
  signInTestUser();
  getAuthState(); // start listening to Firebase, as the app does
  const task = jest.fn();
  addBeforeSignOutTask(task);
  const seen = meResponses([401, 401]);

  await expect(getMe()).rejects.toMatchObject({ kind: 'http', status: 401 } satisfies Partial<ApiError>);

  expect(seen).toHaveLength(2);
  await waitFor(() => expect(mockFirebase.currentUser).toBeNull());
  expect(task).toHaveBeenCalledTimes(1);
  await waitFor(() => expect(getAuthState()).toMatchObject({ status: 'signedOut', sessionEnded: true }));
});

test('a 401 while signed out is not retried', async () => {
  const seen = meResponses([401]);

  await expect(getMe()).rejects.toMatchObject({ status: 401 });
  expect(seen).toHaveLength(1);
});

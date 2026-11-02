import { renderHook, waitFor } from '@testing-library/react-native';
import { delay, http, HttpResponse } from 'msw';

import { ApiError } from '@/api/errors';
import { useHealthQuery } from '@/api/health';
import { queryKeys } from '@/api/query-keys';
import { apiUrl, createTestQueryClient, queryWrapper, setupApiServer } from '../test-utils/api';

const server = setupApiServer();

async function renderHealthQuery() {
  const client = createTestQueryClient();
  const hook = await renderHook(() => useHealthQuery(), { wrapper: queryWrapper(client) });
  return { client, ...hook };
}

/** Answers GET /health with `respond` and counts the requests. */
function countHealthRequests(respond: () => Response) {
  const counter = { calls: 0 };
  server.use(
    http.get(apiUrl('/health'), () => {
      counter.calls += 1;
      return respond();
    }),
  );
  return counter;
}

describe('health query', () => {
  test('caches the result under the shared health key', async () => {
    countHealthRequests(() => HttpResponse.json({ status: 'ok' }));
    const { result, client } = await renderHealthQuery();

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data).toEqual({ status: 'ok' });
    expect(client.getQueryData(queryKeys.health())).toEqual({ status: 'ok' });
  });

  test('each test gets a fresh QueryClient, so nothing is cached from the last test', async () => {
    const { client } = await renderHealthQuery();
    expect(client.getQueryData(queryKeys.health())).toBeUndefined();
  });
});

describe('retries', () => {
  test('4xx is not retried', async () => {
    const counter = countHealthRequests(() =>
      HttpResponse.json({ error: { code: 'bad_request', message: 'Bad.' } }, { status: 400 }),
    );
    const { result } = await renderHealthQuery();

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toMatchObject({ kind: 'http', status: 400 });
    expect(counter.calls).toBe(1);
  });

  test('5xx is retried twice, three requests in all', async () => {
    const counter = countHealthRequests(() =>
      HttpResponse.json({ error: { code: 'internal', message: 'Oops.' } }, { status: 500 }),
    );
    const { result } = await renderHealthQuery();

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toMatchObject({ kind: 'http', status: 500 });
    expect(counter.calls).toBe(3);
  });

  test('network errors are retried twice', async () => {
    const counter = countHealthRequests(() => HttpResponse.error());
    const { result } = await renderHealthQuery();

    await waitFor(() => expect(result.current.isError).toBe(true));
    expect(result.current.error).toBeInstanceOf(ApiError);
    expect(result.current.error).toMatchObject({ kind: 'network' });
    expect(counter.calls).toBe(3);
  });

  test('a 5xx that then succeeds ends in success', async () => {
    let calls = 0;
    server.use(
      http.get(apiUrl('/health'), () => {
        calls += 1;
        return calls === 1
          ? HttpResponse.json({ error: { code: 'unavailable', message: 'Down.' } }, { status: 503 })
          : HttpResponse.json({ status: 'ok' });
      }),
    );
    const { result } = await renderHealthQuery();

    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(calls).toBe(2);
  });
});

describe('cancellation', () => {
  test('unmounting the screen aborts its in-flight request', async () => {
    let reached = false;
    let aborted = false;
    server.use(
      http.get(apiUrl('/health'), async ({ request }) => {
        reached = true;
        request.signal.addEventListener('abort', () => {
          aborted = true;
        });
        await delay('infinite');
        return HttpResponse.json({ status: 'ok' });
      }),
    );
    const { unmount } = await renderHealthQuery();

    await waitFor(() => expect(reached).toBe(true));
    expect(aborted).toBe(false);
    await unmount();

    await waitFor(() => expect(aborted).toBe(true));
  });
});

import { delay, http, HttpResponse } from 'msw';

import { apiClient, callApi, createApiClient, REQUEST_TIMEOUT_MS } from '@/api/client';
import { ApiError } from '@/api/errors';
import { API_URL, apiUrl, setupApiServer } from '../test-utils/api';

const server = setupApiServer();

async function getHealth(client = apiClient, signal?: AbortSignal) {
  return callApi(() => client.GET('/health', { signal }));
}

async function caught(promise: Promise<unknown>): Promise<ApiError> {
  try {
    await promise;
  } catch (error) {
    expect(error).toBeInstanceOf(ApiError);
    return error as ApiError;
  }
  throw new Error('Expected the request to fail');
}

describe('successful calls', () => {
  test('GET /health returns the typed body', async () => {
    server.use(http.get(apiUrl('/health'), () => HttpResponse.json({ status: 'ok' })));

    const health = await getHealth();
    const status: 'ok' = health.status;
    expect(status).toBe('ok');
  });

  test.each([API_URL, `${API_URL}/`, `${API_URL}//`])(
    'base URL %s builds the same request URL',
    async (baseUrl) => {
      const urls: string[] = [];
      server.use(
        http.get(apiUrl('/health'), ({ request }) => {
          urls.push(request.url);
          return HttpResponse.json({ status: 'ok' });
        }),
      );

      await getHealth(createApiClient({ baseUrl }));
      expect(urls).toEqual([`${API_URL}/health`]);
    },
  );
});

describe('request headers', () => {
  test('headers from the header hook are sent with every request', async () => {
    let seen: string | null = null;
    server.use(
      http.get(apiUrl('/health'), ({ request }) => {
        seen = request.headers.get('x-test-header');
        return HttpResponse.json({ status: 'ok' });
      }),
    );

    await getHealth(createApiClient({ baseUrl: API_URL, getHeaders: async () => ({ 'X-Test-Header': 'yes' }) }));
    expect(seen).toBe('yes');
  });

  test('the default hook adds no Authorization header yet', async () => {
    let auth: string | null = 'unset';
    server.use(
      http.get(apiUrl('/health'), ({ request }) => {
        auth = request.headers.get('authorization');
        return HttpResponse.json({ status: 'ok' });
      }),
    );

    await getHealth();
    expect(auth).toBeNull();
  });
});

describe('error kinds', () => {
  test('network failure is a network error', async () => {
    server.use(http.get(apiUrl('/health'), () => HttpResponse.error()));

    const error = await caught(getHealth());
    expect(error.kind).toBe('network');
    expect(error.status).toBeUndefined();
    expect(error.timedOut).toBe(false);
  });

  test('no response before the timeout is a network error', async () => {
    server.use(
      http.get(apiUrl('/health'), async () => {
        await delay('infinite');
        return HttpResponse.json({ status: 'ok' });
      }),
    );

    const error = await caught(getHealth(createApiClient({ baseUrl: API_URL, timeoutMs: 50 })));
    expect(error.kind).toBe('network');
    expect(error.timedOut).toBe(true);
  });

  test('a body that stalls after the headers arrive also times out', async () => {
    // A stub fetch rather than MSW: aborting a half-sent MSW response trips an
    // assertion inside Node's HTTP parser. Like a real fetch, the stub errors
    // the body stream when the request's signal aborts.
    const realFetch = globalThis.fetch;
    globalThis.fetch = async (_input: RequestInfo | URL, init?: RequestInit) => {
      const body = new ReadableStream({
        start(controller) {
          controller.enqueue(new TextEncoder().encode('{"status":'));
          init?.signal?.addEventListener('abort', () =>
            controller.error(new DOMException('This operation was aborted', 'AbortError')),
          );
        },
      });
      return new Response(body, { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
    try {
      const error = await caught(getHealth(createApiClient({ baseUrl: API_URL, timeoutMs: 100 })));
      expect(error.kind).toBe('network');
      expect(error.timedOut).toBe(true);
    } finally {
      globalThis.fetch = realFetch;
    }
  });

  test('a 204 with no body succeeds with no data', async () => {
    server.use(http.get(apiUrl('/health'), () => new HttpResponse(null, { status: 204 })));

    await expect(getHealth()).resolves.toBeUndefined();
  });

  test('the default timeout is 15 seconds', async () => {
    expect(REQUEST_TIMEOUT_MS).toBe(15_000);

    jest.useFakeTimers({ doNotFake: ['queueMicrotask', 'nextTick', 'setImmediate'] });
    try {
      server.use(
        http.get(apiUrl('/health'), async () => {
          await new Promise(() => {});
          return HttpResponse.json({ status: 'ok' });
        }),
      );
      let settled = false;
      const result = caught(getHealth()).finally(() => {
        settled = true;
      });

      await jest.advanceTimersByTimeAsync(REQUEST_TIMEOUT_MS - 1);
      expect(settled).toBe(false);

      await jest.advanceTimersByTimeAsync(1);
      const error = await result;
      expect(error.kind).toBe('network');
      expect(error.timedOut).toBe(true);
    } finally {
      jest.useRealTimers();
    }
  });

  test('aborting the signal cancels the request', async () => {
    server.use(
      http.get(apiUrl('/health'), async () => {
        await delay('infinite');
        return HttpResponse.json({ status: 'ok' });
      }),
    );
    const controller = new AbortController();

    const result = caught(getHealth(apiClient, controller.signal));
    controller.abort();
    const error = await result;
    expect(error.kind).toBe('network');
    expect(error.timedOut).toBe(false);
  });

  test('4xx with the spec error body is an http error with code and message', async () => {
    server.use(
      http.get(apiUrl('/health'), () =>
        HttpResponse.json({ error: { code: 'not_found', message: 'Household not found.' } }, { status: 404 }),
      ),
    );

    const error = await caught(getHealth());
    expect(error).toMatchObject({
      kind: 'http',
      status: 404,
      code: 'not_found',
      serverMessage: 'Household not found.',
    });
  });

  test('500 with the spec error body is an http error', async () => {
    server.use(
      http.get(apiUrl('/health'), () =>
        HttpResponse.json({ error: { code: 'internal', message: 'Something went wrong.' } }, { status: 500 }),
      ),
    );

    const error = await caught(getHealth());
    expect(error).toMatchObject({ kind: 'http', status: 500, code: 'internal' });
  });

  test('an error body that is JSON in another shape is an http error without a code', async () => {
    server.use(http.get(apiUrl('/health'), () => HttpResponse.json({ message: 'nope' }, { status: 400 })));

    const error = await caught(getHealth());
    expect(error).toMatchObject({ kind: 'http', status: 400, code: undefined });
  });

  test('a 502 with an HTML body is unexpected', async () => {
    server.use(
      http.get(apiUrl('/health'), () => HttpResponse.html('<html><body>Bad Gateway</body></html>', { status: 502 })),
    );

    const error = await caught(getHealth());
    expect(error).toMatchObject({ kind: 'unexpected', status: 502, code: undefined });
  });

  test('a success response that is not valid JSON is unexpected', async () => {
    server.use(
      http.get(
        apiUrl('/health'),
        () => new HttpResponse('{"status": "ok"', { status: 200, headers: { 'Content-Type': 'application/json' } }),
      ),
    );

    const error = await caught(getHealth());
    expect(error.kind).toBe('unexpected');
  });
});

// Never called: these lines only have to type-check (or fail to, as marked).
export function typeChecks() {
  // @ts-expect-error a path that is not in the spec
  apiClient.GET('/not-in-the-spec');
  // @ts-expect-error a method the spec does not define for /health
  apiClient.POST('/health', {});
  // @ts-expect-error /health takes no request body
  apiClient.GET('/health', { body: { status: 'ok' } });
}

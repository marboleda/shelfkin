// The app's one way to call the API.
//
// `createApiClient` wraps an openapi-fetch client typed from `schema.ts` (generated
// from openapi/openapi.yaml), so a path, parameter or body that is not in the spec
// is a type error. Every call goes through `callApi()`, which returns the response
// data or throws an `ApiError`:
//
//   const health = await callApi(() => apiClient.GET('/health', { signal }));
//
// Pass TanStack Query's `signal` so leaving a screen cancels the request.

import createClient, { type Client } from 'openapi-fetch';

import { apiConfig, normalizeBaseUrl } from './config';
import { ApiError, errorFromResponse } from './errors';
import { getRequestHeaders, type RequestHeaders } from './headers';
import type { paths } from './schema';

/** A request that gets no response in this time fails as a `network` error. */
export const REQUEST_TIMEOUT_MS = 15_000;

export type ApiClient = Client<paths>;

type ApiClientOptions = {
  baseUrl: string;
  /** Headers added to every request. Defaults to `getRequestHeaders` in headers.ts. */
  getHeaders?: () => RequestHeaders | Promise<RequestHeaders>;
  timeoutMs?: number;
};

export function createApiClient({
  baseUrl,
  getHeaders = getRequestHeaders,
  timeoutMs = REQUEST_TIMEOUT_MS,
}: ApiClientOptions): ApiClient {
  const client = createClient<paths>({
    baseUrl: normalizeBaseUrl(baseUrl),
    fetch: (req) => fetchWithTimeout(req, timeoutMs),
  });
  client.use({
    async onRequest({ request }) {
      const headers = await getHeaders();
      for (const [name, value] of Object.entries(headers)) {
        request.headers.set(name, value);
      }
      return request;
    },
  });
  return client;
}

/** Statuses whose responses cannot have a body. */
const NULL_BODY_STATUSES = new Set([101, 204, 205, 304]);

/**
 * Calls `fetch` and reads the whole body, failing with a `network` ApiError
 * when there is no complete response: the server is unreachable, the caller's
 * signal aborted, or `timeoutMs` passed. The timeout covers the body too, so a
 * server that sends headers and then stalls cannot hang the request.
 */
async function fetchWithTimeout(request: Request, timeoutMs: number): Promise<Response> {
  const controller = new AbortController();
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeoutMs);
  const abortFromCaller = () => controller.abort();
  if (request.signal.aborted) {
    controller.abort();
  } else {
    request.signal.addEventListener('abort', abortFromCaller);
  }

  try {
    const response = await fetch(request, { signal: controller.signal });
    // The API only sends JSON (or, from proxies, text such as HTML), so the body is read as text.
    const body = NULL_BODY_STATUSES.has(response.status) ? null : await response.text();
    return new Response(body, {
      status: response.status,
      statusText: response.statusText,
      headers: response.headers,
    });
  } catch (cause) {
    if (timedOut) {
      throw new ApiError('network', `No response within ${timeoutMs} ms`, { timedOut: true, cause });
    }
    if (request.signal.aborted) {
      throw new ApiError('network', 'Request cancelled', { cause });
    }
    throw new ApiError('network', 'No response from the server', { cause });
  } finally {
    clearTimeout(timer);
    request.signal.removeEventListener('abort', abortFromCaller);
  }
}

type FetchResult<Data> = {
  data?: Data;
  error?: unknown;
  response: Response;
};

/**
 * Runs one openapi-fetch call and returns its data, or throws an `ApiError`
 * (`network`, `http` or `unexpected`) for any failure.
 */
export async function callApi<Data>(send: () => Promise<FetchResult<Data>>): Promise<Data> {
  let result: FetchResult<Data>;
  try {
    result = await send();
  } catch (cause) {
    if (cause instanceof ApiError) throw cause;
    // A success response whose body is not valid JSON, or a failing header hook.
    throw new ApiError('unexpected', 'Could not read the response', { cause });
  }
  const { data, error, response } = result;
  if (!response.ok) {
    throw errorFromResponse(response.status, error);
  }
  return data as Data;
}

/**
 * The client the app uses, pointed at `EXPO_PUBLIC_API_URL`. When the URL is
 * missing in a production build, the root layout shows a configuration error
 * instead of the app. The placeholder `.invalid` address can never resolve, so
 * nothing is ever sent to a real server by mistake.
 */
export const apiClient: ApiClient = createApiClient({
  baseUrl: apiConfig.ok ? apiConfig.baseUrl : 'https://api-url-not-configured.invalid',
});

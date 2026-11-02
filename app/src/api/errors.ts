import type { components } from './schema';

type ErrorBody = components['schemas']['Error'];

/**
 * - `network`: no response at all (offline, server down, timed out).
 * - `http`: the server answered with an error status. `code` and `serverMessage`
 *   are set when the body uses the spec's error format.
 * - `unexpected`: anything else, such as a 502 with an HTML body or a response
 *   that is not valid JSON.
 */
export type ApiErrorKind = 'network' | 'http' | 'unexpected';

type ApiErrorDetails = {
  /** HTTP status, when there was a response. */
  status?: number;
  /** The spec's machine-readable error `code`, e.g. `not_found`. */
  code?: string;
  /** The spec's error `message`. Readable, but not meant for end users; use `userMessage()`. */
  serverMessage?: string;
  /** True when a `network` error was caused by the request timing out. */
  timedOut?: boolean;
  cause?: unknown;
};

/** The one error type every failed API call throws. */
export class ApiError extends Error {
  readonly kind: ApiErrorKind;
  readonly status?: number;
  readonly code?: string;
  readonly serverMessage?: string;
  readonly timedOut: boolean;

  constructor(kind: ApiErrorKind, message: string, details: ApiErrorDetails = {}) {
    super(message, details.cause === undefined ? undefined : { cause: details.cause });
    this.name = 'ApiError';
    this.kind = kind;
    this.status = details.status;
    this.code = details.code;
    this.serverMessage = details.serverMessage;
    this.timedOut = details.timedOut ?? false;
  }
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError;
}

function isErrorBody(body: unknown): body is ErrorBody {
  if (typeof body !== 'object' || body === null || !('error' in body)) return false;
  const detail = (body as { error: unknown }).error;
  return (
    typeof detail === 'object' &&
    detail !== null &&
    typeof (detail as { code?: unknown }).code === 'string' &&
    typeof (detail as { message?: unknown }).message === 'string'
  );
}

/**
 * Turns an error response into an `ApiError`. `body` is what openapi-fetch
 * returns as `error`: parsed JSON, the raw text when it is not JSON, or
 * `undefined` when the body is empty.
 */
export function errorFromResponse(status: number, body: unknown): ApiError {
  if (isErrorBody(body)) {
    return new ApiError('http', `HTTP ${status}: ${body.error.code}`, {
      status,
      code: body.error.code,
      serverMessage: body.error.message,
    });
  }
  if (typeof body === 'string' && body.length > 0) {
    // openapi-fetch hands back the raw text when the body is not JSON.
    return new ApiError('unexpected', `HTTP ${status} with a body that is not JSON`, { status });
  }
  // Empty body, or JSON in another shape: still an HTTP error, just without a code.
  return new ApiError('http', `HTTP ${status}`, { status });
}

/** Whether a failed query should be tried again: network errors and 5xx, never 4xx. */
export function isRetryableError(error: unknown): boolean {
  if (!isApiError(error)) return false;
  if (error.kind === 'network') return true;
  return error.status !== undefined && error.status >= 500;
}

/**
 * A short message for people using the app. Screens show this instead of the
 * error itself, so they never show status codes or stack traces.
 */
export function userMessage(error: unknown): string {
  if (!isApiError(error)) {
    return 'Something went wrong. Please try again.';
  }
  if (error.kind === 'network') {
    return error.timedOut
      ? 'The server took too long to respond. Please try again.'
      : "Can't reach the server. Check your connection and try again.";
  }
  const status = error.status ?? 0;
  if (status === 401) return 'Your session has expired. Please sign in again.';
  if (status === 403) return "You don't have access to this.";
  if (status === 404) return "We couldn't find that. It may have been removed.";
  if (status === 409) return 'This was changed by someone else. Refresh and try again.';
  if (status >= 500) return 'The server ran into a problem. Please try again in a moment.';
  if (status >= 400) return "That didn't work. Check the details and try again.";
  return 'Something went wrong. Please try again.';
}

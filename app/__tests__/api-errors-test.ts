import { ApiError, errorFromResponse, isRetryableError, userMessage } from '@/api/errors';
import { shouldRetryQuery } from '@/api/query-client';

describe('errorFromResponse', () => {
  test('a body in the spec error format gives an http error with code and message', () => {
    expect(errorFromResponse(403, { error: { code: 'forbidden', message: 'No.' } })).toMatchObject({
      kind: 'http',
      status: 403,
      code: 'forbidden',
      serverMessage: 'No.',
    });
  });

  test('an empty body gives an http error with only the status', () => {
    expect(errorFromResponse(404, undefined)).toMatchObject({ kind: 'http', status: 404, code: undefined });
  });

  test('a body that is not JSON is unexpected', () => {
    expect(errorFromResponse(502, '<html>Bad Gateway</html>')).toMatchObject({ kind: 'unexpected', status: 502 });
  });
});

describe('retries', () => {
  const network = new ApiError('network', 'down');
  const http400 = new ApiError('http', 'bad', { status: 400 });
  const http503 = new ApiError('http', 'unavailable', { status: 503 });
  const html502 = new ApiError('unexpected', 'html', { status: 502 });

  test('network errors and 5xx are retried; 4xx and other errors are not', () => {
    expect(isRetryableError(network)).toBe(true);
    expect(isRetryableError(http503)).toBe(true);
    expect(isRetryableError(html502)).toBe(true);
    expect(isRetryableError(http400)).toBe(false);
    expect(isRetryableError(new ApiError('unexpected', 'bad json'))).toBe(false);
    expect(isRetryableError(new Error('boom'))).toBe(false);
  });

  test('at most twice', () => {
    expect(shouldRetryQuery(0, network)).toBe(true);
    expect(shouldRetryQuery(1, network)).toBe(true);
    expect(shouldRetryQuery(2, network)).toBe(false);
    expect(shouldRetryQuery(0, http400)).toBe(false);
  });
});

describe('userMessage', () => {
  const cases: [string, unknown][] = [
    ['network', new ApiError('network', 'down')],
    ['timeout', new ApiError('network', 'slow', { timedOut: true })],
    ['400', new ApiError('http', 'HTTP 400', { status: 400, code: 'bad_request', serverMessage: 'Body is not JSON.' })],
    ['401', new ApiError('http', 'HTTP 401', { status: 401 })],
    ['403', new ApiError('http', 'HTTP 403', { status: 403 })],
    ['404', new ApiError('http', 'HTTP 404', { status: 404 })],
    ['500', new ApiError('http', 'HTTP 500', { status: 500, code: 'internal' })],
    ['502 html', new ApiError('unexpected', 'HTTP 502 with a body that is not JSON', { status: 502 })],
    ['unexpected', new ApiError('unexpected', 'Could not read the response')],
    ['plain Error', new Error('TypeError: x is undefined\n    at foo.js:1')],
    ['not an error', 'oops'],
  ];

  test.each(cases)('%s gives a short message with no status code or stack', (_name, error) => {
    const message = userMessage(error);
    expect(message.length).toBeGreaterThan(0);
    expect(message.length).toBeLessThan(80);
    expect(message).not.toMatch(/\d{3}|HTTP|Error|\n/);
  });

  test('network errors say the server cannot be reached', () => {
    expect(userMessage(new ApiError('network', 'down'))).toMatch(/Can't reach the server/);
  });
});

// Pins the generated API types (app/src/api/schema.ts) that the app relies
// on. If a field used here is renamed or removed in openapi/openapi.yaml,
// `npm --prefix app run typecheck` fails after `npm run generate`.

import type { components, operations } from '@/api/schema';

type HealthOk = operations['getHealth']['responses'][200]['content']['application/json'];
type HealthUnavailable = operations['getHealth']['responses'][503]['content']['application/json'];
type ApiError = components['schemas']['Error'];

// Compile-time equality check: `Expect<Equal<A, B>>` only type-checks when A and B match exactly.
type Equal<A, B> = (<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2 ? true : false;
type Expect<T extends true> = T;

export type HealthOkShape = Expect<Equal<HealthOk, { status: 'ok' }>>;
export type ErrorShape = Expect<Equal<ApiError, { error: { code: string; message: string } }>>;
export type HealthUnavailableShape = Expect<Equal<HealthUnavailable, ApiError>>;

describe('generated API types', () => {
  test('GET /health 200 body is { status: "ok" }', () => {
    const body: HealthOk = { status: 'ok' };
    // @ts-expect-error status only allows "ok"
    const wrong: HealthOk = { status: 'down' };

    expect(body.status).toBe('ok');
    expect(wrong).toBeDefined();
  });

  test('errors use { error: { code, message } }', () => {
    const body: HealthUnavailable = { error: { code: 'unavailable', message: 'The database is unavailable.' } };

    expect(body.error.code).toBe('unavailable');
  });
});

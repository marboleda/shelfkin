import { apiConfig, LOCAL_API_URL, normalizeBaseUrl, resolveApiConfig } from '@/api/config';

// The app's `.env.example` must stay in the repo and document both environments.
const fs: { readFileSync(path: string, encoding: 'utf8'): string } = jest.requireActual('fs');

describe('API base URL', () => {
  test('uses EXPO_PUBLIC_API_URL when it is set', () => {
    expect(resolveApiConfig('https://api.example.com', false)).toEqual({
      ok: true,
      baseUrl: 'https://api.example.com',
    });
    expect(resolveApiConfig('http://10.0.2.2:8080', true)).toEqual({ ok: true, baseUrl: 'http://10.0.2.2:8080' });
  });

  test('a trailing slash makes no difference', () => {
    expect(resolveApiConfig('https://api.example.com/', false)).toEqual(
      resolveApiConfig('https://api.example.com', false),
    );
    expect(normalizeBaseUrl('https://api.example.com/v1//')).toBe('https://api.example.com/v1');
  });

  test('falls back to the local API in development', () => {
    expect(resolveApiConfig(undefined, true)).toEqual({ ok: true, baseUrl: 'http://localhost:8080' });
    expect(resolveApiConfig('', true)).toEqual({ ok: true, baseUrl: LOCAL_API_URL });
  });

  test('is a configuration error in a production build without the variable', () => {
    for (const value of [undefined, '', '  ']) {
      const config = resolveApiConfig(value, false);
      expect(config.ok).toBe(false);
      expect(!config.ok && config.message).toMatch(/EXPO_PUBLIC_API_URL/);
    }
  });

  test('tests run against the local API', () => {
    expect(apiConfig).toEqual({ ok: true, baseUrl: LOCAL_API_URL });
  });

  test('.env.example covers local and production', () => {
    const example = fs.readFileSync('.env.example', 'utf8');
    expect(example).toMatch(/^EXPO_PUBLIC_API_URL=http:\/\/localhost:8080$/m);
    expect(example).toMatch(/10\.0\.2\.2/);
    expect(example).toMatch(/Production/);
  });
});

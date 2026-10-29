# Shelfkin app

The Expo (React Native) app for web, iOS and Android. See the [repository README](../README.md) for setup and tests, and [AGENTS.md](AGENTS.md) for Expo conventions.

## API URL

The app reads the API base URL from `EXPO_PUBLIC_API_URL`. Expo loads it from `.env` files in `app/` and builds it into the bundle, so restart `npx expo start` and reload the app after changing it. [`.env.example`](.env.example) lists the values for each environment; put yours in `app/.env.local`, which is gitignored.

| Where the app runs | `EXPO_PUBLIC_API_URL` |
|---|---|
| Web, or iOS simulator, on the same computer as the API | Unset, or `http://localhost:8080` |
| Android emulator | `http://10.0.2.2:8080`. The emulator's `localhost` is the emulator itself; `10.0.2.2` is your computer |
| Physical device (Expo Go or a development build) | `http://<your computer's LAN IP>:8080`, e.g. `http://192.168.1.20:8080`. The phone and computer must be on the same network, and the firewall must allow port 8080. Find the IP with `ipconfig` (Windows), `ipconfig getifaddr en0` (macOS) or `hostname -I` (Linux) |
| Production builds | The `shelfkin-api` Cloud Run URL (`terraform output api_url`), set by the web and mobile build pipelines |

A trailing slash makes no difference. When the variable is unset, development builds use `http://localhost:8080`, and production builds show an "App not configured" screen instead of calling a server.

The browser blocks the web app's requests to a local API on another origin until the API allows it (CORS, #43). Use a native target or the iOS simulator until then.

## Calling the API

All API code lives in `src/api/`:

| File | What it is |
|---|---|
| `schema.ts` | Types generated from `openapi/openapi.yaml`. Do not edit; run `npm run generate` from the repository root |
| `client.ts` | `apiClient`, a typed [openapi-fetch](https://openapi-ts.dev/openapi-fetch/) client, and `callApi()`, which returns the data or throws an `ApiError`. Requests time out after 15 seconds |
| `headers.ts` | `getRequestHeaders()`, the one place to add request headers (sign-in adds the auth token here) |
| `errors.ts` | `ApiError` (kind `network`, `http` or `unexpected`) and `userMessage()`, the short text screens show for an error |
| `query-keys.ts` | The query key factory. Every query and invalidation uses these keys |
| `query-client.ts` | The `QueryClient` defaults: network errors and 5xx are retried at most twice, 4xx never |
| `query-provider.tsx` | The provider the root layout wraps the app in |
| `health.ts` | `useHealthQuery()`, an example of a feature query |

A feature query looks like this. Passing `signal` cancels the request when its screen goes away:

```ts
export function useHealthQuery() {
  return useQuery({
    queryKey: queryKeys.health(),
    queryFn: ({ signal }) => callApi(() => apiClient.GET('/health', { signal })),
  });
}
```

Show `userMessage(error)` to people, never the error itself.

## Tests

`npm test` runs Jest with the `jest-expo` preset. API calls are mocked with [MSW](https://mswjs.io/); see `test-utils/api.tsx`. `jest.config.js` and `jest/` make MSW work under jest-expo: they compile MSW's ES modules and swap Expo's native `fetch` for Node's, which MSW can intercept.

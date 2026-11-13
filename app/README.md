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

## Sign-in and the Auth emulator

People sign in with email and password through Firebase Auth (the `firebase` JS SDK, which works in Expo Go). In development the app never talks to a real Firebase project: it uses the Firebase Auth emulator from `compose.yaml` and its `demo-shelfkin` project.

| Variable | Development | Production builds |
|---|---|---|
| `EXPO_PUBLIC_FIREBASE_API_KEY`, `EXPO_PUBLIC_FIREBASE_AUTH_DOMAIN`, `EXPO_PUBLIC_FIREBASE_PROJECT_ID` | Leave unset: the app uses `demo-shelfkin` (the emulator accepts any API key) | Required, from `terraform output firebase_web_config` (set by the web and mobile build pipelines, #39 and #41) |
| `EXPO_PUBLIC_FIREBASE_AUTH_EMULATOR_HOST` | `host:port` of the emulator; the app connects to it when set | Must be unset |

A production build that is missing a Firebase value, or has the emulator variable set, shows an "App not configured" screen instead of signing in to the wrong project.

To run the app against the local API and emulator:

1. From the repository root, start Postgres and the emulator with `npm run db:up`, apply migrations with `npm run db:migrate`, and run the API with `FIREBASE_PROJECT_ID=demo-shelfkin` and `FIREBASE_AUTH_EMULATOR_HOST=localhost:9099` (see [Running the API](../README.md#running-the-api)).
2. In `app/.env.local`, set the API URL and the emulator host for where the app runs. The emulator host is the same computer as the API, on port 9099 (or `SHELFKIN_AUTH_PORT`):

   | Where the app runs | `EXPO_PUBLIC_API_URL` | `EXPO_PUBLIC_FIREBASE_AUTH_EMULATOR_HOST` |
   |---|---|---|
   | Web, or iOS simulator | `http://localhost:8080` | `localhost:9099` |
   | Android emulator | `http://10.0.2.2:8080` | `10.0.2.2:9099` |
   | Physical device | `http://<LAN IP>:8080` | `<LAN IP>:9099`. The firewall must allow port 9099 as well as 8080 |

3. Restart `npx expo start` and reload the app, then create an account on `/sign-up`.

The emulator's users live in memory, so they are gone when it stops. **Password reset emails** are not sent: the emulator prints the reset link in its log (`docker compose logs auth`, look for "To reset the password for …"), and lists it at `http://localhost:9099/emulator/v1/projects/demo-shelfkin/oobCodes`. The printed link uses the emulator's port inside Docker (9099); change it if you set `SHELFKIN_AUTH_PORT`.

The emulator has no email enumeration protection (production has it on), so it returns `auth/user-not-found` / `auth/wrong-password` where production returns `auth/invalid-credential`. The app shows "Email or password is incorrect" for all of them.

### How auth works in the app

All auth code lives in `src/auth/`:

| File | What it is |
|---|---|
| `session.ts` | The one source of auth state. `useAuth()` returns `loading`, `signedOut` or `signedIn` (with the user). Also `signIn`, `signUp`, `sendPasswordReset`, `signOut`, `getIdToken`, and the sign-out hook points below |
| `firebase.ts`, `firebase.native.ts` | Creates the Firebase Auth instance: browser local persistence on web (so other tabs follow a sign-out), AsyncStorage on iOS and Android |
| `config.ts` | Reads the `EXPO_PUBLIC_FIREBASE_*` variables |
| `guards.tsx` | `SignedInOnly` and `SignedOutOnly`, used by the `(app)`, `(setup)` and `(guest)` layouts. A signed-out user who opens a signed-in route goes to `/sign-in?redirect=<route>` and returns there after signing in |
| `profile.ts` | `ProfileSync` (after each sign-in and app start: `GET /me`, and `PATCH /me` when the device time zone changed or a sign-up name is waiting), `useMeQuery`, `useUpdateNameMutation` |
| `pending-profile.ts` | The name entered at sign-up, kept on the device until `PATCH /me` succeeds |
| `errors.ts`, `validation.ts` | Readable messages for Firebase errors (never the raw `auth/...` code) and the form rules |

Every API request carries `Authorization: Bearer <Firebase ID token>` (`src/api/headers.ts`). Firebase refreshes the token before it expires. On a 401 the client gets a fresh token and retries once; a second 401 signs the user out, and `/sign-in` says the session ended.

**Sign-out hook points.** Features that must clean up on sign-out register here instead of changing the Sign out button:

```ts
import { addBeforeSignOutTask, onSignedOut } from '@/auth/session';

// Runs before Firebase signs out, while the ID token still works (e.g. remove the push token, #34).
const remove = addBeforeSignOutTask(async () => { /* ... */ });

// Runs after any sign-out: the button, an ended session, or another browser tab.
onSignedOut(() => { /* e.g. clear the persisted cache, #35 */ });
```

A failing task is logged and does not stop the sign-out. The query cache is already cleared this way (`src/api/query-provider.tsx`).

## Calling the API

All API code lives in `src/api/`:

| File | What it is |
|---|---|
| `schema.ts` | Types generated from `openapi/openapi.yaml`. Do not edit; run `npm run generate` from the repository root |
| `client.ts` | `apiClient`, a typed [openapi-fetch](https://openapi-ts.dev/openapi-fetch/) client, and `callApi()`, which returns the data or throws an `ApiError`. Requests time out after 15 seconds |
| `headers.ts` | `getRequestHeaders()`, the one place to add request headers. It adds the signed-in user's Firebase ID token |
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

Firebase Auth and AsyncStorage are replaced by in-memory fakes (`jest/firebase-auth-mock.ts`, mapped in `jest.config.js`), so tests never reach Firebase. Every test starts signed out (`jest/setup-auth.js`). For a test that renders the signed-in app, call `signInTestUser()` and answer its API calls with `setupApiServer(...signedInApiHandlers())`, both from `test-utils/auth.ts`. `mockFirebase` there can also add accounts, make the next Firebase call fail with a given code, sign out as another tab would, or hold the session restore at start.

RNTL 14's `fireEvent` and `render` are async: await them. Avoid async callbacks in `waitFor`; they leave an `act()` open and break later tests in the file.

import type { QueryClient } from '@tanstack/react-query';
import { act, fireEvent, within } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { http, HttpResponse } from 'msw';

import { SERVER_STATUS_TEXT } from '@/components/server-status';
import { apiUrl, setupApiServer } from '../test-utils/api';

jest.setTimeout(60000);

// The root layout builds its QueryClient with createQueryClient(). Use the test
// version (no retry delay, no GC timers) and keep each one, to check that every
// test renders with a fresh client.
const mockCreatedClients: QueryClient[] = [];
jest.mock('@/api/query-client', () => {
  const actual = jest.requireActual('@/api/query-client');
  return {
    ...actual,
    createQueryClient: jest.fn(() => {
      // The same settings as createTestQueryClient() in test-utils/api.tsx.
      const client = actual.createQueryClient({ retryDelay: 0, gcTime: Infinity });
      mockCreatedClients.push(client);
      return client;
    }),
  };
});

const APP_DIR = './src/app';
const server = setupApiServer();

function healthOk() {
  return http.get(apiUrl('/health'), () => HttpResponse.json({ status: 'ok' }));
}

function apiStopped() {
  return http.get(apiUrl('/health'), () => HttpResponse.error());
}

function statusText() {
  return screen.getByTestId('server-status-text');
}

async function openSettings() {
  await renderRouter(APP_DIR, { initialUrl: '/settings' });
  expect(within(screen.getByTestId('server-status')).getByText('Server')).toBeTruthy();
}

async function pressNav(label: string) {
  await act(async () => {
    fireEvent.press(within(screen.getByTestId('bottom-tab-bar')).getByText(label));
  });
}

test('shows a loading state while the health request is in flight, then Connected', async () => {
  let release!: () => void;
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  server.use(
    http.get(apiUrl('/health'), async () => {
      await gate;
      return HttpResponse.json({ status: 'ok' });
    }),
  );

  await openSettings();
  expect(statusText()).toHaveTextContent(SERVER_STATUS_TEXT.checking);

  release();
  expect(await screen.findByText(SERVER_STATUS_TEXT.connected)).toBeTruthy();
});

test('shows Connected when the health endpoint responds', async () => {
  server.use(healthOk());

  await openSettings();

  expect(await screen.findByText(SERVER_STATUS_TEXT.connected)).toBeTruthy();
});

test("shows Can't reach the server when the API is stopped", async () => {
  server.use(apiStopped());

  await openSettings();

  expect(await screen.findByText(SERVER_STATUS_TEXT.unreachable)).toBeTruthy();
});

test('when the API answers with an error, shows a short message, not the status code', async () => {
  server.use(
    http.get(apiUrl('/health'), () =>
      HttpResponse.json({ error: { code: 'unavailable', message: 'The database is unavailable.' } }, { status: 503 }),
    ),
  );

  await openSettings();

  expect(await screen.findByText(/server ran into a problem/)).toBeTruthy();
  expect(statusText()).not.toHaveTextContent(/503|unavailable/);
});

test('after the API comes back, reopening Settings shows Connected again', async () => {
  server.use(apiStopped());
  await openSettings();
  expect(await screen.findByText(SERVER_STATUS_TEXT.unreachable)).toBeTruthy();

  await pressNav('Pantry');
  server.use(healthOk());
  await pressNav('Settings');

  expect(await screen.findByText(SERVER_STATUS_TEXT.connected)).toBeTruthy();
});

test('every test renders the app with a fresh QueryClient', () => {
  // One client per render above, none shared.
  expect(mockCreatedClients.length).toBeGreaterThanOrEqual(5);
  expect(new Set(mockCreatedClients).size).toBe(mockCreatedClients.length);
});

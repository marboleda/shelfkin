import { act, fireEvent, waitFor } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { http, HttpResponse } from 'msw';

import type { components } from '@/api/schema';
import { ApiError, errorFromResponse } from '@/api/errors';
import {
  deleteAccountErrorMessage,
  householdsToTransfer,
  transferOwnershipMessage,
} from '@/auth/delete-account';
import { addBeforeSignOutTask, type SignOutContext } from '@/auth/session';
import { apiUrl, setupApiServer } from '../test-utils/api';
import { mockFirebase, signedInApiHandlers, signInTestUser, testHousehold, testMe } from '../test-utils/auth';

jest.setTimeout(60000);

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

jest.mock('@/auth/timezone', () => ({ deviceTimeZone: () => 'America/Toronto' }));

type Member = components['schemas']['Member'];

const APP_DIR = './src/app';
const ME = testMe();
const HOME = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000a', name: 'Home', role: 'member' });
const CABIN = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000b', name: 'Cabin', role: 'owner', memberCount: 2 });
const CABIN_MEMBERS: Member[] = [
  { userId: ME.id, displayName: 'Sam', email: ME.email, role: 'owner', joinedAt: '2026-10-01T10:00:00Z' },
  { userId: '0190f3a1-0000-7000-8000-0000000000a2', displayName: 'Alex', email: null, role: 'member', joinedAt: '2026-10-02T10:00:00Z' },
];

const server = setupApiServer(
  ...signedInApiHandlers(ME, [HOME, CABIN]),
  http.get(apiUrl(`/households/${CABIN.id}/members`), () => HttpResponse.json({ members: CABIN_MEMBERS })),
  http.get(apiUrl(`/households/${CABIN.id}/invites`), () => HttpResponse.json({ invites: [] })),
);

/** `DELETE /me` answering with `respond`, counting the requests. */
function deleteMeHandler(respond: () => Response = () => new HttpResponse(null, { status: 204 })) {
  const api = { deletes: 0, signedInDuringDelete: [] as boolean[] };
  server.use(
    http.delete(apiUrl('/me'), () => {
      api.deletes += 1;
      api.signedInDuringDelete.push(mockFirebase.currentUser !== null);
      return respond();
    }),
  );
  return api;
}

function mustTransferResponse(households: { id: string; name: string }[]) {
  return HttpResponse.json(
    {
      error: {
        code: 'must_transfer_ownership',
        message: `Transfer ownership of ${households.map((h) => `"${h.name}"`).join(', ')} to another member before deleting your account.`,
        households,
      },
    },
    { status: 409 },
  );
}

async function openDeleteDialog() {
  await renderRouter(APP_DIR, { initialUrl: '/settings' });
  await waitFor(() => expect(screen.getByTestId('account-name-value')).toHaveTextContent('Sam'));
  await act(async () => {
    await fireEvent.press(screen.getByRole('button', { name: 'Delete account' }));
  });
  expect(screen.getByTestId('delete-account-dialog')).toBeTruthy();
}

async function typeAndConfirm(word = 'DELETE') {
  await fireEvent.changeText(screen.getByTestId('delete-account-dialog-input'), word);
  await act(async () => {
    await fireEvent.press(screen.getByTestId('delete-account-dialog-confirm'));
  });
}

beforeEach(() => {
  signInTestUser();
});

describe('Delete account', () => {
  test('the confirmation says what will be deleted, and needs DELETE typed before it can be confirmed', async () => {
    const api = deleteMeHandler();
    await openDeleteDialog();

    const dialog = screen.getByTestId('delete-account-dialog');
    expect(dialog).toHaveTextContent(/permanently deletes your Shelfkin account and your sign-in/);
    expect(dialog).toHaveTextContent(/leave every household/);
    expect(dialog).toHaveTextContent(/reminders stop on all your devices/);
    expect(dialog).toHaveTextContent(/only member are deleted with all their pantry entries/);
    expect(dialog).toHaveTextContent(/can't be undone/);
    expect(screen.getByTestId('delete-account-dialog-confirm')).toBeDisabled();

    await fireEvent.changeText(screen.getByTestId('delete-account-dialog-input'), 'delete');
    expect(screen.getByTestId('delete-account-dialog-confirm')).toBeDisabled();
    await act(async () => {
      await fireEvent.press(screen.getByTestId('delete-account-dialog-confirm'));
    });
    expect(api.deletes).toBe(0);
  });

  test('Cancel closes the dialog and deletes nothing', async () => {
    const api = deleteMeHandler();
    await openDeleteDialog();

    await act(async () => {
      await fireEvent.press(screen.getByTestId('delete-account-dialog-cancel'));
    });

    await waitFor(() => expect(screen.queryByTestId('delete-account-dialog')).toBeNull());
    expect(api.deletes).toBe(0);
    expect(mockFirebase.currentUser).not.toBeNull();
  });

  test('a successful deletion signs out without the usual API calls and lands on /sign-in', async () => {
    const contexts: SignOutContext[] = [];
    addBeforeSignOutTask((context) => {
      contexts.push(context);
    });
    const api = deleteMeHandler();
    await openDeleteDialog();

    await typeAndConfirm();

    await waitFor(() => expect(screen).toHavePathname('/sign-in'));
    expect(api.deletes).toBe(1);
    expect(api.signedInDuringDelete).toEqual([true]);
    expect(mockFirebase.currentUser).toBeNull();
    expect(contexts).toEqual([{ accountDeleted: true }]);
    expect(screen.getByTestId('account-deleted')).toHaveTextContent('Your account has been deleted.');
    expect(screen.queryByTestId('session-ended')).toBeNull();
    expect(screen).toHaveSearchParams({});
  });

  test('must_transfer_ownership names the household and links to its settings', async () => {
    const api = deleteMeHandler(() => mustTransferResponse([{ id: CABIN.id, name: CABIN.name }]));
    await openDeleteDialog();

    await typeAndConfirm();

    const message = await screen.findByTestId('must-transfer-ownership');
    expect(screen.queryByTestId('delete-account-dialog')).toBeNull();
    expect(message).toHaveTextContent(/You own "Cabin", which has other members/);
    expect(message).toHaveTextContent(/Make another member its owner/);
    expect(api.deletes).toBe(1);
    expect(mockFirebase.currentUser).not.toBeNull();

    await act(async () => {
      await fireEvent.press(screen.getByRole('button', { name: 'Open Cabin settings' }));
    });

    await waitFor(() => expect(screen).toHavePathname('/settings/household'));
    // Cabin is now the current household, so its settings (with Transfer ownership) are shown.
    await waitFor(() => expect(screen.getByTestId('household-settings-name')).toHaveTextContent('Cabin'));
    expect(await screen.findByText('Alex')).toBeTruthy();
    expect(screen.getByLabelText('Transfer ownership')).toBeTruthy();
  });

  test('a failed Firebase deletion (503) keeps the dialog open with a message, still signed in', async () => {
    deleteMeHandler(() =>
      HttpResponse.json(
        { error: { code: 'account_deletion_failed', message: 'Your account could not be deleted right now.' } },
        { status: 503 },
      ),
    );
    await openDeleteDialog();

    await typeAndConfirm();

    expect(await screen.findByTestId('delete-account-dialog-error')).toHaveTextContent(
      "Your account couldn't be deleted right now. Nothing was deleted; please try again later.",
    );
    expect(screen.getByTestId('delete-account-dialog')).toBeTruthy();
    expect(mockFirebase.currentUser).not.toBeNull();
    expect(screen).toHavePathname('/settings');
  });
});

describe('delete-account helpers', () => {
  test('householdsToTransfer reads the households of a must_transfer_ownership 409 only', () => {
    const households = [
      { id: 'a', name: 'Cabin' },
      { id: 'b', name: 'Home' },
    ];
    const conflict = errorFromResponse(409, {
      error: { code: 'must_transfer_ownership', message: 'Transfer first.', households },
    });
    expect(householdsToTransfer(conflict)).toEqual(households);
    expect(householdsToTransfer(errorFromResponse(409, { error: { code: 'must_transfer_ownership', message: 'x' } }))).toEqual([]);
    expect(householdsToTransfer(errorFromResponse(409, { error: { code: 'conflict', message: 'x' } }))).toBeNull();
    expect(householdsToTransfer(new ApiError('network', 'down'))).toBeNull();
    expect(householdsToTransfer(new Error('x'))).toBeNull();
  });

  test('transferOwnershipMessage names every household', () => {
    expect(transferOwnershipMessage([{ id: 'a', name: 'Cabin' }])).toBe(
      'You own "Cabin", which has other members. Make another member its owner before deleting your account.',
    );
    expect(
      transferOwnershipMessage([
        { id: 'a', name: 'Cabin' },
        { id: 'b', name: 'Home' },
        { id: 'c', name: 'Lake' },
      ]),
    ).toBe(
      'You own "Cabin", "Home" and "Lake", which have other members. Make another member the owner of each before deleting your account.',
    );
    expect(transferOwnershipMessage([])).toMatch(/Transfer its ownership to another member/);
  });

  test('deleteAccountErrorMessage explains account_deletion_failed and falls back to userMessage', () => {
    expect(deleteAccountErrorMessage(errorFromResponse(503, { error: { code: 'account_deletion_failed', message: 'x' } }))).toMatch(
      /Nothing was deleted/,
    );
    expect(deleteAccountErrorMessage(new ApiError('network', 'down'))).toMatch(/Can't reach the server/);
  });
});

import AsyncStorage from '@react-native-async-storage/async-storage';
import { act, fireEvent, waitFor, within } from '@testing-library/react-native';
import * as Clipboard from 'expo-clipboard';
import { renderRouter, screen } from 'expo-router/testing-library';
import { http, HttpResponse } from 'msw';
import { Dimensions, Share } from 'react-native';

import type { Invite, Member } from '@/household/settings';
import { rememberedHouseholdKey } from '@/household/storage';
import { apiUrl, setupApiServer } from '../test-utils/api';
import { signedInApiHandlers, signInTestUser, testHousehold, testMe } from '../test-utils/auth';

jest.setTimeout(120000);

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

jest.mock('expo-clipboard', () => ({ setStringAsync: jest.fn(() => Promise.resolve(true)) }));
const mockSetString = Clipboard.setStringAsync as jest.MockedFunction<typeof Clipboard.setStringAsync>;

const APP_DIR = './src/app';

const ME = testMe({ displayName: 'Alex', email: 'alex@example.com' });
const HOME = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000a', name: 'Home', role: 'owner' });
const CABIN = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000b', name: 'Cabin', role: 'member' });

const ALEX: Member = { userId: ME.id, displayName: 'Alex', email: 'alex@example.com', role: 'owner', joinedAt: '2026-10-01T10:00:00Z' };
const SAM: Member = { userId: '0190f3a1-0000-7000-8000-0000000000a2', displayName: 'Sam', email: 'sam@example.com', role: 'member', joinedAt: '2026-10-28T10:00:00Z' };
const RILEY: Member = { userId: '0190f3a1-0000-7000-8000-0000000000a3', displayName: 'Riley', email: null, role: 'member', joinedAt: '2026-10-30T10:00:00Z' };

const DAY = 86_400_000;

function invite(id: string, code: string, createdAgoMs: number, expiresInMs: number): Invite {
  const now = Date.now();
  return {
    id: `0190f3a1-0000-7000-8000-0000000001${id}`,
    code,
    url: `https://shelfkin.web.app/join/${code}`,
    createdAt: new Date(now - createdAgoMs).toISOString(),
    expiresAt: new Date(now + expiresInMs).toISOString(),
  };
}

const server = setupApiServer(...signedInApiHandlers(ME, [HOME]));

type Requests = { method: string; path: string; body?: unknown }[];

/**
 * A fake API for one household: members, invites and the actions on them,
 * recording every request. `households` is what `GET /households` returns.
 */
function fakeHousehold({
  households = [HOME],
  members = [ALEX, SAM, RILEY],
  invites = [] as Invite[],
}: { households?: (typeof HOME)[]; members?: Member[]; invites?: Invite[] } = {}) {
  const api = {
    households,
    members,
    invites,
    requests: [] as Requests,
    householdGets: 0,
    /** Set to answer the next owner action with 403 `owner_only`. */
    ownerOnly: false,
    nextInviteCode: 'NEWC0DE2',
  };
  const base = '/households/:householdId';
  const record = (method: string, path: string, body?: unknown) => api.requests.push({ method, path, body });
  const forbidden = () => HttpResponse.json({ error: { code: 'owner_only', message: 'Owner only.' } }, { status: 403 });

  server.use(
    http.get(apiUrl('/households'), () => {
      api.householdGets += 1;
      return HttpResponse.json({ households: api.households });
    }),
    http.get(apiUrl(`${base}/members`), () => {
      record('GET', 'members');
      return HttpResponse.json({ members: api.members });
    }),
    http.get(apiUrl(`${base}/invites`), () => {
      record('GET', 'invites');
      if (api.ownerOnly) return forbidden();
      return HttpResponse.json({ invites: api.invites });
    }),
    http.post(apiUrl(`${base}/invites`), () => {
      record('POST', 'invites');
      if (api.ownerOnly) return forbidden();
      const created = invite('99', api.nextInviteCode, 0, 7 * DAY);
      api.invites = [created, ...api.invites];
      return HttpResponse.json(created, { status: 201 });
    }),
    http.delete(apiUrl(`${base}/invites/:inviteId`), ({ params }) => {
      record('DELETE', `invites/${params.inviteId}`);
      if (api.ownerOnly) return forbidden();
      api.invites = api.invites.filter((item) => item.id !== params.inviteId);
      return new HttpResponse(null, { status: 204 });
    }),
    http.patch(apiUrl(base), async ({ request, params }) => {
      const body = (await request.json()) as { name: string };
      record('PATCH', 'household', body);
      if (api.ownerOnly) return forbidden();
      const updated = { ...api.households.find((item) => item.id === params.householdId)!, name: body.name };
      api.households = api.households.map((item) => (item.id === updated.id ? updated : item));
      return HttpResponse.json(updated);
    }),
    http.delete(apiUrl(base), ({ params }) => {
      record('DELETE', 'household');
      if (api.ownerOnly) return forbidden();
      api.households = api.households.filter((item) => item.id !== params.householdId);
      return new HttpResponse(null, { status: 204 });
    }),
    http.delete(apiUrl(`${base}/members/:userId`), ({ params }) => {
      record('DELETE', `members/${params.userId}`);
      if (api.ownerOnly && params.userId !== ME.id) return forbidden();
      api.members = api.members.filter((item) => item.userId !== params.userId);
      if (params.userId === ME.id) api.households = api.households.filter((item) => item.id !== params.householdId);
      return new HttpResponse(null, { status: 204 });
    }),
    http.post(apiUrl(`${base}/transfer-ownership`), async ({ request }) => {
      const body = (await request.json()) as { userId: string };
      record('POST', 'transfer-ownership', body);
      if (api.ownerOnly) return forbidden();
      const newOwner = api.members.find((item) => item.userId === body.userId)!;
      api.members = [
        { ...newOwner, role: 'owner' },
        ...api.members.filter((item) => item.userId !== body.userId).map((item) => ({ ...item, role: 'member' as const })),
      ];
      api.households = api.households.map((item) => (item.id === HOME.id ? { ...item, role: 'member' } : item));
      return HttpResponse.json({ members: api.members });
    }),
  );
  return api;
}

async function setWindowWidth(width: number) {
  const metrics = { width, height: 900, scale: 1, fontScale: 1 };
  await act(async () => {
    Dimensions.set({ window: metrics, screen: metrics });
  });
}

beforeEach(async () => {
  const { uid } = signInTestUser();
  // Home is current, even when Cabin (first by name) is in the list too.
  await AsyncStorage.setItem(rememberedHouseholdKey(uid), HOME.id);
  mockSetString.mockClear();
  mockSetString.mockResolvedValue(true);
  await setWindowWidth(390);
});

async function press(element: Parameters<typeof fireEvent.press>[0]) {
  await fireEvent.press(element);
}

async function openSettings() {
  await renderRouter(APP_DIR, { initialUrl: '/settings/household' });
  await screen.findByTestId('member-list');
}

function dialog() {
  return within(screen.getByTestId('confirm-dialog'));
}

const OWNER_CONTROLS = ['household-rename', 'create-invite', 'transfer-ownership', 'delete-household'];

describe('Settings entry point', () => {
  test('Settings has a Household section with the name, and Manage household opens /settings/household', async () => {
    fakeHousehold();
    await renderRouter(APP_DIR, { initialUrl: '/settings' });

    const section = await screen.findByTestId('household-section');
    expect(within(section).getByRole('heading', { name: 'Household' })).toBeTruthy();
    expect(within(section).getByTestId('household-section-name')).toHaveTextContent('Home');

    await press(screen.getByTestId('manage-household'));
    expect(await screen.findByTestId('member-list')).toBeTruthy();
    expect(screen).toHavePathname('/settings/household');
  });
});

describe('members and roles', () => {
  test('lists members owner first, with email, Owner badge and (you); the owner sees every owner control', async () => {
    const api = fakeHousehold({ invites: [invite('01', 'ABCDEFGH', 0, 7 * DAY)] });
    await openSettings();

    const rows = within(screen.getByTestId('member-list'));
    expect(rows.getByTestId(`member-${ALEX.userId}`)).toHaveTextContent(/Alex \(you\).*Owner.*alex@example.com/);
    expect(rows.getByTestId(`member-${SAM.userId}`)).toHaveTextContent(/Sam.*sam@example.com/);
    expect(rows.getByTestId(`member-${RILEY.userId}`)).toHaveTextContent(/^Riley/);
    expect(rows.getAllByTestId('owner-badge')).toHaveLength(1);
    const order = screen.getByTestId('member-list').children.map((child) => (child as { props: { testID?: string } }).props.testID);
    expect(order).toEqual([`member-${ALEX.userId}`, `member-${SAM.userId}`, `member-${RILEY.userId}`]);

    for (const id of OWNER_CONTROLS) expect(screen.getByTestId(id)).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Remove Sam' })).toBeTruthy();
    expect(screen.queryByRole('button', { name: 'Remove Alex' })).toBeNull();
    expect(await screen.findByText('ABCD-EFGH')).toBeTruthy();
    expect(api.requests.filter((r) => r.path === 'invites')).toHaveLength(1);

    // The owner cannot leave.
    expect(screen.getByTestId('leave-household')).toBeDisabled();
    expect(screen.getByTestId('owner-leave-hint')).toHaveTextContent(
      'Transfer ownership to another member before leaving, or delete the household',
    );
  });

  test('a member sees none of the owner controls, and no invite request is made', async () => {
    const api = fakeHousehold({ households: [{ ...HOME, role: 'member' }], members: [{ ...SAM, role: 'owner' }, { ...ALEX, role: 'member' }] });
    await openSettings();

    for (const id of OWNER_CONTROLS) expect(screen.queryByTestId(id)).toBeNull();
    expect(screen.queryByText('Remove')).toBeNull();
    expect(screen.queryByText('Invites')).toBeNull();
    expect(screen.getByTestId('leave-household')).toBeEnabled();
    expect(screen.queryByTestId('owner-leave-hint')).toBeNull();
    expect(api.requests.some((r) => r.path.startsWith('invites'))).toBe(false);
  });

  test('when the owner is the only member, transfer is not offered and the screen explains why', async () => {
    fakeHousehold({ members: [ALEX] });
    await openSettings();

    expect(screen.queryByTestId('transfer-ownership')).toBeNull();
    expect(screen.getByTestId('transfer-unavailable')).toHaveTextContent(/Someone must join Home before you can transfer ownership/);
  });
});

describe('member actions', () => {
  test('leave: after confirming, the app switches to another household and says "You left Home"', async () => {
    const api = fakeHousehold({
      households: [{ ...HOME, role: 'member' }, CABIN],
      members: [{ ...SAM, role: 'owner' }, { ...ALEX, role: 'member' }],
    });
    await openSettings();

    await press(screen.getByTestId('leave-household'));
    expect(dialog().getByText("Leave Home? You'll lose access to its pantry.")).toBeTruthy();
    await press(dialog().getByTestId('confirm-dialog-confirm'));

    expect(await screen.findByLabelText('Switch household, current: Cabin')).toBeTruthy();
    expect(screen).toHavePathname('/');
    expect(screen.getByTestId('household-notice')).toHaveTextContent(/You left Home/);
    expect(api.requests).toContainEqual({ method: 'DELETE', path: `members/${ME.id}`, body: undefined });
  });

  test('leaving the last household goes to onboarding', async () => {
    fakeHousehold({ households: [{ ...HOME, role: 'member' }], members: [{ ...SAM, role: 'owner' }, { ...ALEX, role: 'member' }] });
    await openSettings();

    await press(screen.getByTestId('leave-household'));
    await press(dialog().getByTestId('confirm-dialog-confirm'));

    expect(await screen.findByRole('heading', { name: 'Set up your pantry' })).toBeTruthy();
    expect(screen).toHavePathname('/onboarding');
  });

  test('cancel closes the dialog without a request', async () => {
    const api = fakeHousehold();
    await openSettings();

    await press(screen.getByRole('button', { name: 'Remove Sam' }));
    await press(dialog().getByTestId('confirm-dialog-cancel'));
    await waitFor(() => expect(screen.queryByTestId('confirm-dialog')).toBeNull());
    expect(api.requests.some((r) => r.method === 'DELETE')).toBe(false);
  });
});

describe('owner actions', () => {
  test('remove asks first, then removes the member from the list', async () => {
    const api = fakeHousehold();
    await openSettings();

    await press(screen.getByRole('button', { name: 'Remove Sam' }));
    expect(dialog().getByText("Remove Sam from Home? They'll lose access to its pantry.")).toBeTruthy();
    await press(dialog().getByTestId('confirm-dialog-confirm'));

    await waitFor(() => expect(screen.queryByTestId(`member-${SAM.userId}`)).toBeNull());
    expect(api.requests).toContainEqual({ method: 'DELETE', path: `members/${SAM.userId}`, body: undefined });
    expect(screen.queryByTestId('confirm-dialog')).toBeNull();
  });

  test('transfer: pick a member, confirm, and the view switches to the member view without a reload', async () => {
    const api = fakeHousehold();
    await openSettings();
    const householdGets = api.householdGets;

    await press(screen.getByTestId('transfer-ownership'));
    expect(dialog().getByTestId('confirm-dialog-confirm')).toBeDisabled();
    await press(dialog().getByRole('radio', { name: 'Sam' }));
    await press(dialog().getByTestId('confirm-dialog-confirm'));
    expect(dialog().getByText("Make Sam the owner? You'll become a member.")).toBeTruthy();
    await press(dialog().getByTestId('confirm-dialog-confirm'));

    await waitFor(() => expect(screen.queryByTestId('household-rename')).toBeNull());
    expect(api.requests).toContainEqual({ method: 'POST', path: 'transfer-ownership', body: { userId: SAM.userId } });
    expect(within(screen.getByTestId(`member-${SAM.userId}`)).getByTestId('owner-badge')).toBeTruthy();
    expect(within(screen.getByTestId(`member-${ALEX.userId}`)).queryByTestId('owner-badge')).toBeNull();
    for (const id of OWNER_CONTROLS) expect(screen.queryByTestId(id)).toBeNull();
    expect(screen.queryByText('Remove')).toBeNull();
    expect(screen.getByTestId('leave-household')).toBeEnabled();
    expect(api.householdGets).toBe(householdGets);
  });

  test('delete needs the household name typed, then goes to onboarding when no household is left', async () => {
    const api = fakeHousehold();
    await openSettings();

    await press(screen.getByTestId('delete-household'));
    const confirm = dialog().getByTestId('confirm-dialog-confirm');
    expect(confirm).toBeDisabled();
    await fireEvent.changeText(dialog().getByTestId('confirm-dialog-input'), 'home');
    expect(confirm).toBeDisabled();
    await press(confirm);
    expect(api.requests.some((r) => r.path === 'household')).toBe(false);

    await fireEvent.changeText(dialog().getByTestId('confirm-dialog-input'), 'Home');
    expect(dialog().getByTestId('confirm-dialog-confirm')).toBeEnabled();
    await press(dialog().getByTestId('confirm-dialog-confirm'));

    expect(await screen.findByRole('heading', { name: 'Set up your pantry' })).toBeTruthy();
    expect(api.requests).toContainEqual({ method: 'DELETE', path: 'household', body: undefined });
  });

  test('delete with another household left switches to it', async () => {
    fakeHousehold({ households: [HOME, CABIN] });
    await openSettings();

    await press(screen.getByTestId('delete-household'));
    await fireEvent.changeText(dialog().getByTestId('confirm-dialog-input'), 'Home');
    await press(dialog().getByTestId('confirm-dialog-confirm'));

    expect(await screen.findByLabelText('Switch household, current: Cabin')).toBeTruthy();
    expect(screen).toHavePathname('/');
  });

  test('a failed action shows the #24 message in the dialog and keeps it open', async () => {
    fakeHousehold();
    server.use(
      http.delete(apiUrl('/households/:householdId/members/:userId'), () =>
        HttpResponse.json({ error: { code: 'internal', message: 'boom' } }, { status: 500 }),
      ),
    );
    await openSettings();

    await press(screen.getByRole('button', { name: 'Remove Sam' }));
    await press(dialog().getByTestId('confirm-dialog-confirm'));

    expect(await screen.findByTestId('confirm-dialog-error')).toHaveTextContent(
      'The server ran into a problem. Please try again in a moment.',
    );
    expect(screen.getByTestId(`member-${SAM.userId}`)).toBeTruthy();
  });

  test('the confirm button shows progress and is disabled while the request runs', async () => {
    fakeHousehold();
    let release: () => void = () => {};
    const gate = new Promise<void>((resolve) => {
      release = resolve;
    });
    server.use(
      http.delete(apiUrl('/households/:householdId/members/:userId'), async () => {
        await gate;
        return new HttpResponse(null, { status: 204 });
      }),
    );
    await openSettings();

    await press(screen.getByRole('button', { name: 'Remove Sam' }));
    await press(dialog().getByTestId('confirm-dialog-confirm'));
    await waitFor(() => expect(dialog().getByTestId('button-busy')).toBeTruthy());
    expect(dialog().getByTestId('confirm-dialog-confirm')).toBeDisabled();
    expect(dialog().getByTestId('confirm-dialog-cancel')).toBeDisabled();

    await act(async () => release());
    await waitFor(() => expect(screen.queryByTestId(`member-${SAM.userId}`)).toBeNull());
  });
});

describe('rename', () => {
  test('validates the name, sends it trimmed, and the new name shows in the Pantry header', async () => {
    const api = fakeHousehold();
    await openSettings();

    await press(screen.getByTestId('household-rename'));
    const input = screen.getByTestId('household-rename-input');
    await fireEvent.changeText(input, '   ');
    await press(screen.getByTestId('household-rename-save'));
    expect(screen.getByText('Enter a name for your household')).toBeTruthy();

    await fireEvent.changeText(input, 'x'.repeat(51));
    await press(screen.getByTestId('household-rename-save'));
    expect(screen.getByText('Use 50 characters or fewer')).toBeTruthy();
    expect(api.requests.some((r) => r.method === 'PATCH')).toBe(false);

    await fireEvent.changeText(input, '  Maple St. Kitchen  ');
    await press(screen.getByTestId('household-rename-save'));

    await waitFor(() => expect(screen.getByTestId('household-settings-name')).toHaveTextContent('Maple St. Kitchen'));
    expect(api.requests).toContainEqual({ method: 'PATCH', path: 'household', body: { name: 'Maple St. Kitchen' } });

    await press(screen.getByRole('tab', { name: 'Pantry' }));
    expect(await screen.findByLabelText('Switch household, current: Maple St. Kitchen')).toBeTruthy();
  });
});

describe('invites', () => {
  test('lists active invites newest first with formatted codes and expiry; an expired one never shows', async () => {
    fakeHousehold({
      invites: [
        invite('01', 'NEWER234', 1 * DAY, 6 * DAY + 60_000),
        invite('02', 'TMRW2345', 6 * DAY, DAY + 60_000),
        invite('03', 'OLDER234', 7 * DAY - 60_000, -1_000),
      ],
    });
    await openSettings();

    const list = within(await screen.findByTestId('invite-list'));
    const codes = list.getAllByLabelText(/^Invite code /).map((node) => node.props.children);
    expect(codes).toEqual(['NEWE-R234', 'TMRW-2345']);
    expect(screen.queryByText('OLDE-R234')).toBeNull();
    expect(list.getByText(/Expires in 6 days|Expires in 7 days/)).toBeTruthy();
    expect(list.getByText(/Expires tomorrow|Expires in 2 days/)).toBeTruthy();
  });

  test('an invite disappears when it expires while the screen is open', async () => {
    const soon = invite('04', 'SOON2345', 7 * DAY - 30_000, 30_000);
    fakeHousehold({ invites: [soon] });
    await openSettings();
    expect(await screen.findByText('SOON-2345')).toBeTruthy();
    expect(screen.getByText('Expires today')).toBeTruthy();

    await act(async () => {
      jest.advanceTimersByTime(31_000);
    });
    expect(screen.queryByText('SOON-2345')).toBeNull();
    expect(screen.getByTestId('no-invites')).toBeTruthy();
  });

  test('Create invite adds the new invite at the top', async () => {
    const api = fakeHousehold({ invites: [invite('01', 'ABCDEFGH', DAY, 6 * DAY)] });
    await openSettings();
    await screen.findByText('ABCD-EFGH');

    await press(screen.getByTestId('create-invite'));
    await waitFor(() =>
      expect(within(screen.getByTestId('invite-list')).getAllByLabelText(/^Invite code /)[0].props.children).toBe('NEWC-0DE2'),
    );
    expect(api.requests.filter((r) => r.method === 'POST' && r.path === 'invites')).toHaveLength(1);
  });

  test('Copy code and Copy link put the code and the url on the clipboard and show "Copied"', async () => {
    const item = invite('01', 'ABCDEFGH', DAY, 6 * DAY);
    fakeHousehold({ invites: [item] });
    await openSettings();
    await screen.findByText('ABCD-EFGH');

    await press(screen.getByRole('button', { name: 'Copy code ABCD-EFGH' }));
    await waitFor(() => expect(mockSetString).toHaveBeenCalledWith('ABCD-EFGH'));
    expect(await screen.findByRole('button', { name: 'Copy code ABCD-EFGH' })).toHaveTextContent('Copied');
    expect(screen.getByRole('status')).toHaveTextContent('Copied');

    await press(screen.getByRole('button', { name: 'Copy link for ABCD-EFGH' }));
    await waitFor(() => expect(mockSetString).toHaveBeenLastCalledWith(item.url));
    expect(await screen.findByRole('button', { name: 'Copy link for ABCD-EFGH' })).toHaveTextContent('Copied');
  });

  test('Share opens the share sheet with the invite message', async () => {
    const share = jest.spyOn(Share, 'share').mockResolvedValue({ action: Share.sharedAction, activityType: undefined });
    const item = invite('01', 'ABCDEFGH', DAY, 6 * DAY);
    fakeHousehold({ invites: [item] });
    await openSettings();
    await screen.findByText('ABCD-EFGH');

    await press(screen.getByRole('button', { name: 'Share ABCD-EFGH' }));
    await waitFor(() =>
      expect(share).toHaveBeenCalledWith({
        message: `Join Home on Shelfkin: ${item.url} (or enter code ABCD-EFGH)`,
      }),
    );
    share.mockRestore();
  });

  test('Revoke asks first, then removes the invite', async () => {
    const item = invite('01', 'ABCDEFGH', DAY, 6 * DAY);
    const api = fakeHousehold({ invites: [item] });
    await openSettings();
    await screen.findByText('ABCD-EFGH');

    await press(screen.getByRole('button', { name: 'Revoke ABCD-EFGH' }));
    expect(api.requests.some((r) => r.method === 'DELETE')).toBe(false);
    await press(dialog().getByTestId('confirm-dialog-confirm'));

    await waitFor(() => expect(screen.queryByText('ABCD-EFGH')).toBeNull());
    expect(api.requests).toContainEqual({ method: 'DELETE', path: `invites/${item.id}`, body: undefined });
  });

  test('revoking an invite that has just expired succeeds without an error', async () => {
    const item = invite('05', 'EXPR2345', 7 * DAY - 5_000, 5_000);
    fakeHousehold({ invites: [item] });
    await openSettings();
    await screen.findByText('EXPR-2345');

    await press(screen.getByRole('button', { name: 'Revoke EXPR-2345' }));
    // It expires while the dialog is open; the API still answers 204.
    await act(async () => {
      jest.advanceTimersByTime(6_000);
    });
    await press(dialog().getByTestId('confirm-dialog-confirm'));

    await waitFor(() => expect(screen.queryByTestId('confirm-dialog')).toBeNull());
    expect(screen.queryByTestId('confirm-dialog-error')).toBeNull();
    expect(screen.queryByText('EXPR-2345')).toBeNull();
  });
});

describe('ownership moved elsewhere', () => {
  test('a 403 owner_only reloads into the member view and says so', async () => {
    const api = fakeHousehold();
    await openSettings();

    // Ownership moved in another session.
    api.ownerOnly = true;
    api.households = [{ ...HOME, role: 'member' }];
    api.members = [{ ...SAM, role: 'owner' }, { ...ALEX, role: 'member' }, RILEY];

    await press(screen.getByTestId('household-rename'));
    await fireEvent.changeText(screen.getByTestId('household-rename-input'), 'Cottage');
    await press(screen.getByTestId('household-rename-save'));

    expect(await screen.findByTestId('ownership-lost')).toHaveTextContent("You're no longer the owner of Home");
    for (const id of OWNER_CONTROLS) expect(screen.queryByTestId(id)).toBeNull();
    expect(within(screen.getByTestId(`member-${SAM.userId}`)).getByTestId('owner-badge')).toBeTruthy();
    expect(screen.getByTestId('leave-household')).toBeEnabled();
  });

  test('a 403 owner_only on a confirmed action closes the dialog and shows the member view', async () => {
    const api = fakeHousehold();
    await openSettings();
    api.ownerOnly = true;
    api.households = [{ ...HOME, role: 'member' }];
    api.members = [{ ...SAM, role: 'owner' }, { ...ALEX, role: 'member' }, RILEY];

    await press(screen.getByRole('button', { name: 'Remove Riley' }));
    await press(dialog().getByTestId('confirm-dialog-confirm'));

    expect(await screen.findByTestId('ownership-lost')).toHaveTextContent("You're no longer the owner of Home");
    await waitFor(() => expect(screen.queryByTestId('confirm-dialog')).toBeNull());
    expect(screen.queryByTestId('delete-household')).toBeNull();
  });

  test('a 403 owner_only when listing invites reloads into the member view', async () => {
    const api = fakeHousehold();
    api.ownerOnly = true;
    server.use(
      http.get(apiUrl('/households'), () => {
        api.householdGets += 1;
        // The first load still says owner; the reload after the 403 says member.
        return HttpResponse.json({ households: api.householdGets === 1 ? [HOME] : [{ ...HOME, role: 'member' }] });
      }),
    );
    await renderRouter(APP_DIR, { initialUrl: '/settings/household' });

    expect(await screen.findByTestId('ownership-lost')).toHaveTextContent("You're no longer the owner of Home");
    expect(screen.queryByTestId('create-invite')).toBeNull();
  });
});

describe('household gone', () => {
  test('a 404 for the members falls back to another household with the #26 message', async () => {
    fakeHousehold({ households: [HOME, CABIN] });
    const api = { households: [HOME, CABIN] };
    server.use(
      http.get(apiUrl('/households'), () => HttpResponse.json({ households: api.households })),
      http.get(apiUrl('/households/:householdId/members'), ({ params }) => {
        if (params.householdId === HOME.id) {
          api.households = [CABIN];
          return HttpResponse.json({ error: { code: 'not_found', message: 'Household not found.' } }, { status: 404 });
        }
        return HttpResponse.json({ members: [{ ...SAM, role: 'owner' }, { ...ALEX, role: 'member' }] });
      }),
    );
    await renderRouter(APP_DIR, { initialUrl: '/settings/household' });

    expect(await screen.findByText('You no longer have access to Home')).toBeTruthy();
    await waitFor(() => expect(screen.getByTestId('household-settings-name')).toHaveTextContent('Cabin'));
  });
});

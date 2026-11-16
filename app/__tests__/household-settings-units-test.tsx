import { fireEvent, render, screen } from '@testing-library/react-native';
import * as Clipboard from 'expo-clipboard';
import { Platform, Share } from 'react-native';

import { ConfirmDialog } from '@/components/confirm-dialog';
import { formatInviteCode, inviteExpiryLabel, inviteShareMessage, isInviteActive, type Invite } from '@/household/settings';
import { shareInvite } from '@/household/share-invite';

jest.mock('expo-clipboard', () => ({ setStringAsync: jest.fn(() => Promise.resolve(true)) }));
const mockSetString = Clipboard.setStringAsync as jest.MockedFunction<typeof Clipboard.setStringAsync>;

const INVITE: Invite = {
  id: '0190f3a1-0000-7000-8000-000000000101',
  code: 'K7MQ3XPA',
  url: 'https://shelfkin.web.app/join/K7MQ3XPA',
  createdAt: '2026-11-14T10:00:00Z',
  expiresAt: '2026-11-21T10:00:00Z',
};

describe('invite formatting', () => {
  test('codes show as two groups of four', () => {
    expect(formatInviteCode('ABCDEFGH')).toBe('ABCD-EFGH');
    expect(formatInviteCode('abcd-efgh')).toBe('ABCD-EFGH');
  });

  test('expiry is counted in calendar days on the device', () => {
    const now = new Date(2026, 10, 14, 9, 0);
    expect(inviteExpiryLabel(new Date(2026, 10, 14, 23, 0).toISOString(), now)).toBe('Expires today');
    expect(inviteExpiryLabel(new Date(2026, 10, 15, 1, 0).toISOString(), now)).toBe('Expires tomorrow');
    expect(inviteExpiryLabel(new Date(2026, 10, 20, 8, 0).toISOString(), now)).toBe('Expires in 6 days');
  });

  test('an invite is not active at or after its expiresAt', () => {
    const expires = new Date(INVITE.expiresAt).getTime();
    expect(isInviteActive(INVITE, expires - 1)).toBe(true);
    expect(isInviteActive(INVITE, expires)).toBe(false);
    expect(isInviteActive(INVITE, expires + 1)).toBe(false);
  });

  test('the share message has the household, link and code', () => {
    expect(inviteShareMessage('Home', INVITE)).toBe(
      'Join Home on Shelfkin: https://shelfkin.web.app/join/K7MQ3XPA (or enter code K7MQ-3XPA)',
    );
  });
});

describe('shareInvite', () => {
  const message = inviteShareMessage('Home', INVITE);
  const nav = globalThis.navigator as unknown as { share?: unknown };
  const originalShare = nav.share;

  afterEach(() => {
    nav.share = originalShare;
    mockSetString.mockClear();
  });

  test('on iOS and Android it opens the system share sheet', async () => {
    const share = jest.spyOn(Share, 'share').mockResolvedValue({ action: Share.sharedAction, activityType: undefined });
    await expect(shareInvite(message, INVITE.url)).resolves.toBe('shared');
    expect(share).toHaveBeenCalledWith({ message });
    share.mockRestore();
  });

  describe('on web', () => {
    beforeEach(() => {
      jest.replaceProperty(Platform, 'OS', 'web');
    });

    test('uses the Web Share API when the browser has it', async () => {
      const webShare = jest.fn(() => Promise.resolve());
      nav.share = webShare;
      await expect(shareInvite(message, INVITE.url)).resolves.toBe('shared');
      expect(webShare).toHaveBeenCalledWith({ text: message });
      expect(mockSetString).not.toHaveBeenCalled();
    });

    test('copies the link when the browser has no Web Share API', async () => {
      nav.share = undefined;
      await expect(shareInvite(message, INVITE.url)).resolves.toBe('copied');
      expect(mockSetString).toHaveBeenCalledWith(INVITE.url);
    });

    test('does nothing more when the user cancels the share sheet', async () => {
      nav.share = jest.fn(() => Promise.reject(Object.assign(new Error('cancelled'), { name: 'AbortError' })));
      await expect(shareInvite(message, INVITE.url)).resolves.toBe('cancelled');
      expect(mockSetString).not.toHaveBeenCalled();
    });

    test('copies the link when the browser refuses to share', async () => {
      nav.share = jest.fn(() => Promise.reject(Object.assign(new Error('nope'), { name: 'NotAllowedError' })));
      await expect(shareInvite(message, INVITE.url)).resolves.toBe('copied');
      expect(mockSetString).toHaveBeenCalledWith(INVITE.url);
    });
  });
});

describe('ConfirmDialog', () => {
  async function renderDialog(props: Partial<Parameters<typeof ConfirmDialog>[0]> = {}) {
    const onConfirm = jest.fn();
    const onCancel = jest.fn();
    await render(
      <ConfirmDialog
        visible
        title="Delete household"
        message="This deletes Home."
        confirmLabel="Delete"
        destructive
        onConfirm={onConfirm}
        onCancel={onCancel}
        {...props}
      />,
    );
    return { onConfirm, onCancel };
  }

  test('confirm and cancel call back', async () => {
    const { onConfirm, onCancel } = await renderDialog();
    expect(screen.getByTestId('confirm-dialog').props.role).toBe('alertdialog');
    expect(screen.getByRole('heading', { name: 'Delete household' })).toBeTruthy();
    expect(screen.getByText('This deletes Home.')).toBeTruthy();
    await fireEvent.press(screen.getByTestId('confirm-dialog-confirm'));
    expect(onConfirm).toHaveBeenCalledTimes(1);
    await fireEvent.press(screen.getByTestId('confirm-dialog-cancel'));
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  test('the escape gesture cancels', async () => {
    const { onCancel } = await renderDialog();
    await fireEvent(screen.getByTestId('confirm-dialog'), 'accessibilityEscape');
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  test('tapping the backdrop cancels, once however often it is tapped', async () => {
    const { onCancel } = await renderDialog();
    await fireEvent(screen.getByTestId('confirm-dialog-backdrop', { includeHiddenElements: true }), 'responderRelease');
    await fireEvent(screen.getByTestId('confirm-dialog-backdrop', { includeHiddenElements: true }), 'responderRelease');
    expect(onCancel).toHaveBeenCalledTimes(1);
  });

  test('while busy, nothing can be pressed or dismissed', async () => {
    const { onConfirm, onCancel } = await renderDialog({ busy: true });
    expect(screen.getByTestId('button-busy')).toBeTruthy();
    expect(screen.getByTestId('confirm-dialog-confirm')).toBeDisabled();
    expect(screen.getByTestId('confirm-dialog-cancel')).toBeDisabled();
    await fireEvent(screen.getByTestId('confirm-dialog'), 'accessibilityEscape');
    await fireEvent(screen.getByTestId('confirm-dialog-backdrop', { includeHiddenElements: true }), 'responderRelease');
    expect(onConfirm).not.toHaveBeenCalled();
    expect(onCancel).not.toHaveBeenCalled();
  });

  test('typeToConfirm keeps the confirm button disabled until the exact text is typed', async () => {
    const { onConfirm } = await renderDialog({ typeToConfirm: { label: 'Type "Home" to confirm', expected: 'Home' } });
    const confirm = screen.getByTestId('confirm-dialog-confirm');
    expect(confirm).toBeDisabled();
    await fireEvent.changeText(screen.getByLabelText('Type "Home" to confirm'), 'Hom');
    expect(confirm).toBeDisabled();
    await fireEvent.changeText(screen.getByLabelText('Type "Home" to confirm'), ' Home ');
    expect(screen.getByTestId('confirm-dialog-confirm')).toBeEnabled();
    await fireEvent.press(screen.getByTestId('confirm-dialog-confirm'));
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  test('shows an error message', async () => {
    await renderDialog({ error: "Can't reach the server. Check your connection and try again." });
    expect(screen.getByTestId('confirm-dialog-error')).toHaveTextContent(/Can't reach the server/);
  });
});

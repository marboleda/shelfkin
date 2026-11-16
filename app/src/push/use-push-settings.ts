// State and actions behind the Notifications section on Settings.

import { useQueryClient } from '@tanstack/react-query';
import { useCallback, useEffect, useState } from 'react';
import { AppState } from 'react-native';

import { userMessage } from '@/api/errors';
import { useMeQuery } from '@/auth/profile';
import { useAuth } from '@/auth/session';

import { canReceivePush, getPushPermission, isNativeApp, type PushPermission, type PushSetupErrorReason } from './device';
import { turnOffAccount, turnOffDevice, turnOnAccount, turnOnDevice, type TurnOnResult } from './registration';
import { loadDevicePush } from './storage';

export const BLOCKED_MESSAGE = 'Notifications are blocked for Shelfkin. Turn them on in Settings.';

export type PushProblem =
  /** The OS permission is denied: show `BLOCKED_MESSAGE` with a button to the system settings. */
  | { kind: 'blocked' }
  | { kind: 'error'; message: string };

const SETUP_MESSAGES: Record<PushSetupErrorReason, string> = {
  not_configured: "This build of Shelfkin isn't set up for push notifications yet.",
  unsupported: 'Reminders need the Shelfkin app on a phone.',
  token_failed: "Couldn't set up notifications on this phone. Check your connection and try again.",
};

function problemOf(result: TurnOnResult): PushProblem | null {
  if (result.ok) return null;
  if (result.reason === 'blocked') return { kind: 'blocked' };
  return { kind: 'error', message: SETUP_MESSAGES[result.reason] };
}

type DeviceState = { uid: string; enabled: boolean; permission: PushPermission };

async function readDevice(uid: string): Promise<DeviceState> {
  const [stored, permission] = await Promise.all([loadDevicePush(uid), getPushPermission()]);
  return { uid, enabled: stored.enabled, permission };
}

export type PushSettings = {
  /** `pushEnabled` from `GET /me`, or undefined while it loads. */
  accountOn: boolean | undefined;
  /** True on iOS and Android, where "On this device" is shown while the account switch is on. */
  showDeviceSwitch: boolean;
  /** False on a simulator or emulator, which cannot receive pushes. */
  deviceSupported: boolean;
  /** The "On this device" switch, or undefined while it loads. Off when notifications are blocked. */
  deviceOn: boolean | undefined;
  /** The OS permission on this phone, or undefined on web and while it loads. */
  permission: PushPermission | undefined;
  /** The switch being changed, if any, and the value it is changing to. */
  pending: { which: 'account' | 'device'; value: boolean } | null;
  problem: PushProblem | null;
  setAccount: (on: boolean) => void;
  setDevice: (on: boolean) => void;
};

export function usePushSettings(): PushSettings {
  const auth = useAuth();
  const uid = auth.status === 'signedIn' ? auth.user.uid : null;
  const queryClient = useQueryClient();
  const me = useMeQuery();
  const native = isNativeApp();
  const [device, setDeviceState] = useState<DeviceState | null>(null);
  const [pending, setPending] = useState<PushSettings['pending']>(null);
  const [problem, setProblem] = useState<PushProblem | null>(null);

  // On open, and whenever the app comes back from the background (the user
  // may have changed the permission in system settings meanwhile).
  useEffect(() => {
    if (!uid || !native) return;
    let cancelled = false;
    const load = () => {
      readDevice(uid)
        .then((state) => {
          if (!cancelled) setDeviceState(state);
        })
        .catch((error) => console.warn('Could not read the notification permission', error));
    };
    load();
    const subscription = AppState.addEventListener('change', (status) => {
      if (status === 'active') load();
    });
    return () => {
      cancelled = true;
      subscription.remove();
    };
  }, [uid, native]);

  const refreshDevice = useCallback(async () => {
    if (!uid || !native) return;
    setDeviceState(await readDevice(uid));
  }, [uid, native]);

  const current = device && device.uid === uid ? device : null;
  // A permission that is no longer granted always reads as off.
  const deviceOn = current ? current.enabled && current.permission === 'granted' : undefined;

  const run = useCallback(
    async (which: 'account' | 'device', value: boolean, action: () => Promise<PushProblem | null>) => {
      setPending({ which, value });
      setProblem(null);
      try {
        setProblem(await action());
      } catch (error) {
        setProblem({ kind: 'error', message: userMessage(error) });
      } finally {
        await refreshDevice().catch(() => {});
        setPending(null);
      }
    },
    [refreshDevice],
  );

  const setAccount = useCallback(
    (on: boolean) => {
      if (!uid || pending) return;
      void run('account', on, async () => {
        if (!on) {
          await turnOffAccount(queryClient);
          return null;
        }
        return problemOf(await turnOnAccount(queryClient, uid));
      });
    },
    [uid, pending, run, queryClient],
  );

  const setDevice = useCallback(
    (on: boolean) => {
      if (!uid || pending) return;
      void run('device', on, async () => {
        if (!on) {
          await turnOffDevice(uid);
          return null;
        }
        return problemOf(await turnOnDevice(uid));
      });
    },
    [uid, pending, run],
  );

  return {
    accountOn: me.data?.pushEnabled,
    showDeviceSwitch: native,
    deviceSupported: canReceivePush(),
    deviceOn,
    permission: current?.permission,
    pending,
    problem,
    setAccount,
    setDevice,
  };
}

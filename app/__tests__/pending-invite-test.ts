// The pending invite code through sign-outs (#33, #107): only the code of a
// join that got a 401 survives the sign-out of the session that 401 ended, and
// that holds whichever runs first, the join screen's save or the listener.

import AsyncStorage from '@react-native-async-storage/async-storage';
import { act } from '@testing-library/react-native';

import { endSession, getAuthState, signOut } from '@/auth/session';
import {
  clearPendingInvite,
  loadPendingInvite,
  PENDING_INVITE_KEY,
  releaseKeptPendingInvite,
  resetPendingInviteForTests,
  savePendingInvite,
} from '@/household/pending-invite';
import { mockFirebase, signInTestUser } from '../test-utils/auth';

const CODE = 'K7QM4XPA';

/** Lets every queued promise (Firebase's fake, the auth listeners, storage) settle. */
async function settle() {
  await act(async () => {
    for (let i = 0; i < 20; i += 1) await Promise.resolve();
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

async function signedIn() {
  signInTestUser();
  getAuthState();
  await settle();
  expect(getAuthState().status).toBe('signedIn');
}

async function stored() {
  await settle();
  return { memory: await loadPendingInvite(), device: await AsyncStorage.getItem(PENDING_INVITE_KEY) };
}

const kept = { memory: CODE, device: CODE };
const cleared = { memory: null, device: null };

test('an ended session keeps a code saved for it before the sign-out listener runs', async () => {
  await signedIn();
  await savePendingInvite(CODE, { keepOnEndedSession: true });
  await endSession();
  expect(await stored()).toEqual(kept);
});

test('an ended session keeps a code saved for it after the sign-out listener ran', async () => {
  await signedIn();
  await endSession();
  await settle();
  expect(getAuthState()).toMatchObject({ status: 'signedOut', sessionEnded: true });
  await savePendingInvite(CODE, { keepOnEndedSession: true });
  expect(await stored()).toEqual(kept);
});

test('the save and the sign-out started together keep the code', async () => {
  await signedIn();
  const ending = endSession();
  const saving = savePendingInvite(CODE, { keepOnEndedSession: true });
  await Promise.all([ending, saving]);
  expect(await stored()).toEqual(kept);
});

test('an ended session clears a code that was not saved for it', async () => {
  await AsyncStorage.setItem(PENDING_INVITE_KEY, CODE);
  resetPendingInviteForTests();
  await signedIn();
  expect(await loadPendingInvite()).toBe(CODE);
  await endSession();
  expect(await stored()).toEqual(cleared);
});

test('an ended session clears a stale code even while another code is marked', async () => {
  await signedIn();
  await savePendingInvite('OTHER222', { keepOnEndedSession: true });
  // A later invite link replaces the code, and with it the mark.
  await savePendingInvite(CODE);
  await endSession();
  expect(await stored()).toEqual(cleared);
});

test('the Sign out button clears a marked code', async () => {
  await signedIn();
  await savePendingInvite(CODE, { keepOnEndedSession: true });
  await signOut();
  expect(await stored()).toEqual(cleared);
});

test('a sign-out in another tab clears a marked code', async () => {
  await signedIn();
  await savePendingInvite(CODE, { keepOnEndedSession: true });
  mockFirebase.signOutElsewhere();
  expect(await stored()).toEqual(cleared);
});

test('a finished join drops the mark, so a later ended session clears a new copy of the code', async () => {
  await signedIn();
  await savePendingInvite(CODE, { keepOnEndedSession: true });
  await clearPendingInvite();
  await savePendingInvite(CODE);
  await endSession();
  expect(await stored()).toEqual(cleared);
});

test('the mark is used once: the next ended session clears the kept code', async () => {
  await signedIn();
  await savePendingInvite(CODE, { keepOnEndedSession: true });
  await endSession();
  expect(await stored()).toEqual(kept);

  await signedIn();
  await endSession();
  expect(await stored()).toEqual(cleared);
});

test('releasing the mark while signed in lets an ended session clear the code', async () => {
  await signedIn();
  await savePendingInvite(CODE, { keepOnEndedSession: true });
  releaseKeptPendingInvite();
  await endSession();
  expect(await stored()).toEqual(cleared);
});

test('releasing the mark after the ended session has signed out keeps the code', async () => {
  await signedIn();
  await savePendingInvite(CODE, { keepOnEndedSession: true });
  const ending = endSession();
  await ending;
  releaseKeptPendingInvite();
  expect(await stored()).toEqual(kept);
});

test('a save marked for an ended session does nothing after a plain sign-out', async () => {
  await signedIn();
  await signOut();
  await settle();
  await savePendingInvite(CODE, { keepOnEndedSession: true });
  expect(await stored()).toEqual(cleared);
});

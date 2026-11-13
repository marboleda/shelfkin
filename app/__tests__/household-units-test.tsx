import AsyncStorage from '@react-native-async-storage/async-storage';
import { fireEvent, render, screen } from '@testing-library/react-native';
import { http, HttpResponse } from 'msw';
import { Pressable, Text } from 'react-native';

import { ApiError } from '@/api/errors';
import { householdIdOfKey, queryKeys } from '@/api/query-keys';
import { CurrentHouseholdProvider, useHouseholdKeys, useHouseholds } from '@/household/current-household';
import {
  householdInitial,
  joinedMessage,
  joinErrorMessage,
  roleLabel,
  sortHouseholds,
  validateHouseholdName,
  validateInviteCode,
} from '@/household/households';
import { isHouseholdItemPath } from '@/household/navigation';
import { rememberedHouseholdKey } from '@/household/storage';
import { apiUrl, createTestQueryClient, queryWrapper, setupApiServer } from '../test-utils/api';
import { householdsHandler, signInTestUser, testHousehold } from '../test-utils/auth';

const HOME = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000a', name: 'Home' });
const CABIN = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000b', name: 'Cabin', role: 'member' });

const server = setupApiServer(householdsHandler([HOME, CABIN]));

describe('household rules', () => {
  test('the initial is the first character of the trimmed name, uppercased', () => {
    expect(householdInitial('home')).toBe('H');
    expect(householdInitial('  cabin ')).toBe('C');
    expect(householdInitial('🏠 Home')).toBe('🏠');
    expect(householdInitial('')).toBe('');
  });

  test('households are sorted by name ignoring case, then by ID', () => {
    const a = testHousehold({ id: 'b', name: 'home' });
    const b = testHousehold({ id: 'a', name: 'Home' });
    const c = testHousehold({ id: 'c', name: 'Apartment' });
    expect(sortHouseholds([a, b, c]).map((item) => item.id)).toEqual(['c', 'a', 'b']);
  });

  test('roles read Owner or Member', () => {
    expect(roleLabel('owner')).toBe('Owner');
    expect(roleLabel('member')).toBe('Member');
  });

  test('the name is trimmed and must be 1 to 50 characters', () => {
    expect(validateHouseholdName('   ')).toBe('Enter a name for your household');
    expect(validateHouseholdName('x'.repeat(51))).toBe('Use 50 characters or fewer');
    expect(validateHouseholdName(` ${'x'.repeat(50)} `)).toBeNull();
    expect(validateHouseholdName('Home')).toBeNull();
  });

  test('an invite code must have something other than spaces and dashes', () => {
    expect(validateInviteCode(' - ')).toBe('Enter the invite code');
    expect(validateInviteCode('k7mq-3xpa')).toBeNull();
  });

  test.each([
    ['invite_not_found', "That code doesn't match an invite. Check it and try again."],
    ['invite_expired', 'This invite has expired. Ask for a new one.'],
    ['invite_revoked', 'This invite is no longer valid. Ask for a new one.'],
  ])('join error %s has its own message', (code, message) => {
    expect(joinErrorMessage(new ApiError('http', 'x', { status: 404, code }))).toBe(message);
  });

  test('join messages', () => {
    expect(joinedMessage({ household: CABIN, alreadyMember: false })).toBe('You joined Cabin');
    expect(joinedMessage({ household: CABIN, alreadyMember: true })).toBe("You're already a member of Cabin");
  });

  test('Add item and Entry detail are item screens; Pantry and Settings are not', () => {
    expect(isHouseholdItemPath('/add')).toBe(true);
    expect(isHouseholdItemPath('/entries/abc')).toBe(true);
    expect(isHouseholdItemPath('/')).toBe(false);
    expect(isHouseholdItemPath('/settings')).toBe(false);
  });

  test('household-scoped keys start with the household ID', () => {
    expect(queryKeys.households.pantry(HOME.id)).toEqual(['households', HOME.id, 'entries']);
    expect(householdIdOfKey(queryKeys.households.members(HOME.id))).toBe(HOME.id);
    expect(householdIdOfKey(queryKeys.households.catalogSearch(HOME.id, 'milk'))).toBe(HOME.id);
    expect(householdIdOfKey(queryKeys.households.list())).toBeNull();
    expect(householdIdOfKey(queryKeys.me())).toBeNull();
  });
});

function Probe() {
  const { state, switchTo } = useHouseholds();
  if (state.status !== 'ready' || !state.current) return <Text>{state.status}</Text>;
  return <KeysProbe onSwitch={switchTo} households={state.households.map((item) => item.id)} />;
}

function KeysProbe({ onSwitch, households }: { onSwitch: (id: string) => void; households: string[] }) {
  const keys = useHouseholdKeys();
  return (
    <>
      <Text testID="pantry-key">{JSON.stringify(keys.pantry())}</Text>
      {households.map((id) => (
        <Pressable key={id} accessibilityRole="button" accessibilityLabel={`switch ${id}`} onPress={() => onSwitch(id)} />
      ))}
    </>
  );
}

async function renderProvider() {
  const client = createTestQueryClient();
  const Wrapper = queryWrapper(client);
  return await render(
    <Wrapper>
      <CurrentHouseholdProvider>
        <Probe />
      </CurrentHouseholdProvider>
    </Wrapper>,
  );
}

describe('current household', () => {
  test('switching changes the household ID in the pantry query key, and is remembered for this user', async () => {
    const user = signInTestUser();
    await renderProvider();

    // "Cabin" sorts before "Home", so it is current with nothing remembered.
    expect(await screen.findByTestId('pantry-key')).toHaveTextContent(JSON.stringify(['households', CABIN.id, 'entries']));

    await fireEvent.press(screen.getByLabelText(`switch ${HOME.id}`));
    expect(screen.getByTestId('pantry-key')).toHaveTextContent(JSON.stringify(['households', HOME.id, 'entries']));
    expect(await AsyncStorage.getItem(rememberedHouseholdKey(user.uid))).toBe(HOME.id);
    expect(rememberedHouseholdKey(user.uid)).toContain(user.uid);
  });

  test('a failed list is an error, not an empty list', async () => {
    server.use(
      http.get(apiUrl('/households'), () =>
        HttpResponse.json({ error: { code: 'bad_request', message: 'x' } }, { status: 400 }),
      ),
    );
    signInTestUser();
    await renderProvider();

    expect(await screen.findByText('error')).toBeTruthy();
  });
});

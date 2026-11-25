import AsyncStorage from '@react-native-async-storage/async-storage';

import {
  DEFAULT_PANTRY_VIEW,
  loadPantryView,
  pantryViewKey,
  parsePantryView,
  savePantryView,
} from '@/pantry/view-storage';

test('a user with nothing saved gets "Location" with no filters', async () => {
  expect(await loadPantryView('uid-1')).toEqual({
    mode: 'location',
    filters: { locationIds: [], categoryIds: [], expiring: false },
  });
});

test('saves and restores the mode and filters, Expiring included, under a key with the user ID', async () => {
  const choices = { mode: 'category' as const, filters: { locationIds: [2, 3], categoryIds: [5], expiring: true } };
  await savePantryView('uid-1', choices);

  expect(pantryViewKey('uid-1')).toBe('shelfkin.pantryView.uid-1');
  expect(JSON.parse((await AsyncStorage.getItem('shelfkin.pantryView.uid-1')) as string)).toEqual({
    mode: 'category',
    locationIds: [2, 3],
    categoryIds: [5],
    expiring: true,
  });
  expect(await loadPantryView('uid-1')).toEqual(choices);
});

test('a second user on the device gets the defaults, and keeps their own choices', async () => {
  await savePantryView('uid-1', { mode: 'category', filters: { locationIds: [2], categoryIds: [], expiring: false } });
  expect(await loadPantryView('uid-2')).toEqual(DEFAULT_PANTRY_VIEW);

  await savePantryView('uid-2', { mode: 'location', filters: { locationIds: [], categoryIds: [7], expiring: true } });
  expect((await loadPantryView('uid-1')).mode).toBe('category');
  expect((await loadPantryView('uid-1')).filters.expiring).toBe(false);
  expect((await loadPantryView('uid-2')).filters).toEqual({ locationIds: [], categoryIds: [7], expiring: true });
});

test('IDs are kept as saved here (the screen ignores unknown ones)', () => {
  expect(parsePantryView('{"mode":"location","locationIds":[999],"categoryIds":[1,1]}')).toEqual({
    mode: 'location',
    filters: { locationIds: [999], categoryIds: [1], expiring: false },
  });
});

test('choices saved before the Expiring chip existed restore with Expiring off', () => {
  expect(parsePantryView('{"mode":"category","locationIds":[2],"categoryIds":[]}')).toEqual({
    mode: 'category',
    filters: { locationIds: [2], categoryIds: [], expiring: false },
  });
});

test.each([
  ['not JSON', '{mode: category'],
  ['not an object', '"category"'],
  ['null', 'null'],
  ['an unknown mode', '{"mode":"shelf","locationIds":[],"categoryIds":[]}'],
  ['IDs that are not a list', '{"mode":"category","locationIds":2,"categoryIds":[]}'],
  ['IDs that are not numbers', '{"mode":"category","locationIds":["2"],"categoryIds":[]}'],
  ['missing IDs', '{"mode":"category"}'],
  ['Expiring that is not true or false', '{"mode":"category","locationIds":[],"categoryIds":[],"expiring":"yes"}'],
])('malformed saved data (%s) falls back to the defaults', async (_, raw) => {
  await AsyncStorage.setItem(pantryViewKey('uid-1'), raw);
  expect(await loadPantryView('uid-1')).toEqual(DEFAULT_PANTRY_VIEW);
});

test('read and write failures are caught', async () => {
  const warn = jest.spyOn(console, 'warn').mockImplementation(() => {});
  const getItem = jest.spyOn(AsyncStorage, 'getItem').mockRejectedValueOnce(new Error('disk'));
  const setItem = jest.spyOn(AsyncStorage, 'setItem').mockRejectedValueOnce(new Error('disk'));

  await expect(loadPantryView('uid-1')).resolves.toEqual(DEFAULT_PANTRY_VIEW);
  await expect(savePantryView('uid-1', DEFAULT_PANTRY_VIEW)).resolves.toBeUndefined();

  getItem.mockRestore();
  setItem.mockRestore();
  warn.mockRestore();
});

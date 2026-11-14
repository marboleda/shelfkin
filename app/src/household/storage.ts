// The last household each user selected on this device. The key includes the
// user ID, so two people sharing a device each get their own.

import AsyncStorage from '@react-native-async-storage/async-storage';

const KEY_PREFIX = 'shelfkin.currentHousehold.';

export function rememberedHouseholdKey(uid: string): string {
  return `${KEY_PREFIX}${uid}`;
}

/** The remembered household ID, or null when there is none or storage cannot be read. */
export async function loadRememberedHousehold(uid: string): Promise<string | null> {
  try {
    return await AsyncStorage.getItem(rememberedHouseholdKey(uid));
  } catch {
    return null;
  }
}

export async function saveRememberedHousehold(uid: string, householdId: string): Promise<void> {
  try {
    await AsyncStorage.setItem(rememberedHouseholdKey(uid), householdId);
  } catch (error) {
    // Not fatal: the app falls back to the first household on the next start.
    console.warn('Could not remember the current household', error);
  }
}

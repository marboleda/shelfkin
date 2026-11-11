// The name entered at sign-up, kept on the device until `PATCH /me` succeeds.
// If the request fails, the profile sync sends it again on the next start.

import AsyncStorage from '@react-native-async-storage/async-storage';

const STORAGE_KEY = 'shelfkin.pendingProfile';

export type PendingProfile = {
  /** The account the name belongs to, so it is never sent for another user. */
  email: string;
  displayName: string;
};

export async function savePendingProfile(profile: PendingProfile): Promise<void> {
  await AsyncStorage.setItem(STORAGE_KEY, JSON.stringify(profile));
}

export async function loadPendingProfile(): Promise<PendingProfile | null> {
  try {
    const raw = await AsyncStorage.getItem(STORAGE_KEY);
    if (!raw) return null;
    const value = JSON.parse(raw) as Partial<PendingProfile>;
    if (typeof value.email !== 'string' || typeof value.displayName !== 'string') return null;
    return { email: value.email, displayName: value.displayName };
  } catch {
    return null;
  }
}

export async function clearPendingProfile(): Promise<void> {
  await AsyncStorage.removeItem(STORAGE_KEY);
}

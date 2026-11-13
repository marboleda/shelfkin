import { Stack } from 'expo-router';

import { SignedInOnly } from '@/auth/guards';
import { HouseholdGate } from '@/components/household-gate';
import { useTheme } from '@/theme';

/**
 * Signed-in screens outside the app shell (no tab bar): /onboarding, for users
 * with no household. Users who have one are sent to Pantry.
 */
export default function SetupLayout() {
  const theme = useTheme();
  return (
    <SignedInOnly>
      <HouseholdGate needs="none">
        <Stack
          screenOptions={{
            headerShown: false,
            contentStyle: { backgroundColor: theme.colors.background },
          }}
        />
      </HouseholdGate>
    </SignedInOnly>
  );
}

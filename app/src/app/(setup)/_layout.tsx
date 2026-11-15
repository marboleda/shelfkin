import { Stack } from 'expo-router';
import { View } from 'react-native';

import { SignedInOnly } from '@/auth/guards';
import { HouseholdGate } from '@/components/household-gate';
import { HouseholdNotice } from '@/components/household-notice';
import { useTheme } from '@/theme';

/**
 * Signed-in screens outside the app shell (no tab bar): /onboarding, for users
 * with no household. Users who have one are sent to Pantry. The household
 * notice is shown here too, so "You no longer have access to <name>" is seen
 * by a user who just lost their only household.
 */
export default function SetupLayout() {
  const theme = useTheme();
  return (
    <SignedInOnly>
      <HouseholdGate needs="none">
        <View style={{ flex: 1 }}>
          <Stack
            screenOptions={{
              headerShown: false,
              contentStyle: { backgroundColor: theme.colors.background },
            }}
          />
          <HouseholdNotice />
        </View>
      </HouseholdGate>
    </SignedInOnly>
  );
}

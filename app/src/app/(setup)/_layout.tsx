import { Stack } from 'expo-router';

import { SignedInOnly } from '@/auth/guards';
import { useTheme } from '@/theme';

/** Signed-in screens outside the app shell (no tab bar): /onboarding. */
export default function SetupLayout() {
  const theme = useTheme();
  return (
    <SignedInOnly>
      <Stack
        screenOptions={{
          headerShown: false,
          contentStyle: { backgroundColor: theme.colors.background },
        }}
      />
    </SignedInOnly>
  );
}

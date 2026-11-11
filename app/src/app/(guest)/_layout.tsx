import { Stack } from 'expo-router';

import { SignedOutOnly } from '@/auth/guards';
import { useTheme } from '@/theme';

/** /sign-in and /sign-up. A signed-in user is sent on to the app. */
export default function GuestLayout() {
  const theme = useTheme();
  return (
    <SignedOutOnly>
      <Stack
        screenOptions={{
          headerShown: false,
          contentStyle: { backgroundColor: theme.colors.background },
        }}
      />
    </SignedOutOnly>
  );
}

import { Tabs } from 'expo-router/js-tabs';
import { View } from 'react-native';

import { SignedInOnly } from '@/auth/guards';
import { AppNav } from '@/components/app-nav';
import { HouseholdGate } from '@/components/household-gate';
import { HouseholdNotice } from '@/components/household-notice';
import { useIsWideLayout } from '@/hooks/use-is-wide-layout';
import { useLeaveItemScreensOnHouseholdChange } from '@/household/navigation';
import { useTheme } from '@/theme';

/**
 * The app shell: Pantry and Settings in a bottom tab bar on narrow windows, or a left
 * side nav on wide ones. Add item and Entry detail live in the same navigator, so the
 * Android back button returns to Pantry (the first route). They have no nav item because
 * `AppNav` only renders `NAV_ITEMS`; `href: null` is not used because it stops the routes
 * from being opened directly by URL. Signed-out users are sent to /sign-in, and users
 * with no household to /onboarding.
 */
export default function AppLayout() {
  return (
    <SignedInOnly>
      <HouseholdGate needs="household">
        <Shell />
      </HouseholdGate>
    </SignedInOnly>
  );
}

function Shell() {
  const wide = useIsWideLayout();
  const theme = useTheme();
  useLeaveItemScreensOnHouseholdChange();

  return (
    <View style={{ flex: 1 }}>
      <Tabs
        backBehavior="firstRoute"
        tabBar={(props) => <AppNav {...props} />}
        screenOptions={{
          headerShown: false,
          tabBarPosition: wide ? 'left' : 'bottom',
          sceneStyle: { backgroundColor: theme.colors.background },
        }}
      >
        <Tabs.Screen name="index" options={{ title: 'Pantry' }} />
        <Tabs.Screen name="settings" options={{ title: 'Settings' }} />
        <Tabs.Screen name="add" options={{ title: 'Add item' }} />
        <Tabs.Screen name="entries/[id]" options={{ title: 'Entry detail' }} />
      </Tabs>
      <HouseholdNotice />
    </View>
  );
}

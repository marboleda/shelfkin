import { Stack, ThemeProvider } from 'expo-router';
import * as SplashScreen from 'expo-splash-screen';
import { StatusBar } from 'expo-status-bar';
import * as SystemUI from 'expo-system-ui';
import { useEffect, useMemo } from 'react';
import { ActivityIndicator, View } from 'react-native';

import { apiConfig } from '@/api/config';
import { QueryProvider } from '@/api/query-provider';
import { firebaseConfig } from '@/auth/config';
import { ProfileSync } from '@/auth/profile';
import { useAuth } from '@/auth/session';
import { ConfigError } from '@/components/config-error';
import { getNavigationTheme, useTheme } from '@/theme';
import { useAppFonts } from '@/theme/fonts';

// Keep the native splash screen up until the fonts are ready and the session is restored,
// so text never renders in the system font and a signed-in user never sees /sign-in flash.
// Not awaited, as the expo-splash-screen docs advise.
SplashScreen.preventAutoHideAsync().catch(() => {});

/** The first configuration problem of this build, if any. */
const configProblem = !apiConfig.ok ? apiConfig.message : !firebaseConfig.ok ? firebaseConfig.message : null;

export default function RootLayout() {
  const theme = useTheme();
  const navigationTheme = useMemo(() => getNavigationTheme(theme), [theme]);
  const fontsReady = useAppFonts();

  // Keep the native root view (visible behind screens and during transitions) on-theme.
  useEffect(() => {
    SystemUI.setBackgroundColorAsync(theme.colors.background).catch(() => {});
  }, [theme.colors.background]);

  // With a configuration problem there is no session to wait for (see AppRoutes).
  useEffect(() => {
    if (fontsReady && configProblem) {
      SplashScreen.hide();
    }
  }, [fontsReady]);

  return (
    <ThemeProvider value={navigationTheme}>
      <StatusBar style={theme.dark ? 'light' : 'dark'} />
      {!fontsReady ? (
        // Behind the splash screen on native; on web (which has no native splash) this keeps
        // the page on the theme background instead of a blank white page.
        <View testID="fonts-loading" style={{ flex: 1, backgroundColor: theme.colors.background }} />
      ) : configProblem ? (
        // A production build without its API URL or Firebase settings, or pointed at the
        // Auth emulator: say so instead of talking to the wrong server or project.
        <ConfigError message={configProblem} />
      ) : (
        <QueryProvider>
          <ProfileSync />
          <AppRoutes />
        </QueryProvider>
      )}
    </ThemeProvider>
  );
}

/** The routes, once Firebase has restored the session. Until then, a loading screen. */
function AppRoutes() {
  const theme = useTheme();
  const auth = useAuth();
  const loading = auth.status === 'loading';

  useEffect(() => {
    if (!loading) {
      SplashScreen.hide();
    }
  }, [loading]);

  if (loading) {
    return (
      <View
        testID="auth-loading"
        aria-busy
        accessibilityLabel="Loading"
        style={{ flex: 1, alignItems: 'center', justifyContent: 'center', backgroundColor: theme.colors.background }}
      >
        <ActivityIndicator size="large" color={theme.colors.primary} />
      </View>
    );
  }

  return (
    <Stack
      screenOptions={{
        headerShown: false,
        contentStyle: { backgroundColor: theme.colors.background },
      }}
    />
  );
}

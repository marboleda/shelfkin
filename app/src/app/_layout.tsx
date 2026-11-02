import { Stack, ThemeProvider } from 'expo-router';
import * as SplashScreen from 'expo-splash-screen';
import { StatusBar } from 'expo-status-bar';
import * as SystemUI from 'expo-system-ui';
import { useEffect, useMemo } from 'react';
import { View } from 'react-native';

import { apiConfig } from '@/api/config';
import { QueryProvider } from '@/api/query-provider';
import { ConfigError } from '@/components/config-error';
import { getNavigationTheme, useTheme } from '@/theme';
import { useAppFonts } from '@/theme/fonts';

// Keep the native splash screen up until the fonts are ready, so text never renders in the
// system font and then jumps to Figtree. Not awaited, as the expo-splash-screen docs advise.
SplashScreen.preventAutoHideAsync().catch(() => {});

export default function RootLayout() {
  const theme = useTheme();
  const navigationTheme = useMemo(() => getNavigationTheme(theme), [theme]);
  const fontsReady = useAppFonts();

  // Keep the native root view (visible behind screens and during transitions) on-theme.
  useEffect(() => {
    SystemUI.setBackgroundColorAsync(theme.colors.background).catch(() => {});
  }, [theme.colors.background]);

  useEffect(() => {
    if (fontsReady) {
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
      ) : apiConfig.ok ? (
        <QueryProvider>
          <Stack
            screenOptions={{
              headerShown: false,
              contentStyle: { backgroundColor: theme.colors.background },
            }}
          />
        </QueryProvider>
      ) : (
        // A production build without EXPO_PUBLIC_API_URL: say so instead of calling the wrong server.
        <ConfigError message={apiConfig.message} />
      )}
    </ThemeProvider>
  );
}

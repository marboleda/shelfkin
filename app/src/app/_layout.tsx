import { Stack, ThemeProvider } from 'expo-router';
import { StatusBar } from 'expo-status-bar';
import * as SystemUI from 'expo-system-ui';
import { useEffect, useMemo } from 'react';

import { apiConfig } from '@/api/config';
import { QueryProvider } from '@/api/query-provider';
import { ConfigError } from '@/components/config-error';
import { getNavigationTheme, useTheme } from '@/theme';

export default function RootLayout() {
  const theme = useTheme();
  const navigationTheme = useMemo(() => getNavigationTheme(theme), [theme]);

  // Keep the native root view (visible behind screens and during transitions) on-theme.
  useEffect(() => {
    SystemUI.setBackgroundColorAsync(theme.colors.background).catch(() => {});
  }, [theme.colors.background]);

  return (
    <ThemeProvider value={navigationTheme}>
      <StatusBar style={theme.dark ? 'light' : 'dark'} />
      {apiConfig.ok ? (
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

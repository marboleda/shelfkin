import Head from 'expo-router/head';
import type { ReactNode } from 'react';
import { ScrollView, Text, View } from 'react-native';
import { useSafeAreaInsets, type EdgeInsets } from 'react-native-safe-area-context';

import { useIsWideLayout } from '@/hooks/use-is-wide-layout';
import { CONTENT_MAX_WIDTH, useThemedStyles, type Theme } from '@/theme';

export const APP_NAME = 'Shelfkin';

export function documentTitle(screenTitle: string): string {
  return `${screenTitle} · ${APP_NAME}`;
}

type ScreenProps = {
  /** Screen name, shown as the page heading and in the browser tab title. */
  title: string;
  /**
   * True for screens rendered inside the app shell. The tab bar / side nav then owns
   * the safe-area edge it sits on, and the screen only pads the remaining edges.
   */
  inShell?: boolean;
  /** Rendered above the heading, e.g. a back control. */
  accessory?: ReactNode;
  children?: ReactNode;
};

export function safeAreaPadding(insets: EdgeInsets, inShell: boolean, wide: boolean) {
  if (!inShell) {
    return {
      paddingTop: insets.top,
      paddingRight: insets.right,
      paddingBottom: insets.bottom,
      paddingLeft: insets.left,
    };
  }
  if (wide) {
    // The side nav covers the left edge.
    return {
      paddingTop: insets.top,
      paddingRight: insets.right,
      paddingBottom: insets.bottom,
    };
  }
  // The bottom tab bar covers the bottom edge.
  return {
    paddingTop: insets.top,
    paddingRight: insets.right,
    paddingLeft: insets.left,
  };
}

export function Screen({ title, inShell = false, accessory, children }: ScreenProps) {
  const styles = useThemedStyles(createStyles);
  const insets = useSafeAreaInsets();
  const wide = useIsWideLayout();

  return (
    <View testID="screen" style={[styles.root, safeAreaPadding(insets, inShell, wide)]}>
      <Head>
        <title>{documentTitle(title)}</title>
      </Head>
      <ScrollView style={styles.scroll} contentContainerStyle={styles.scrollContent}>
        <View testID="screen-content" style={styles.content}>
          {accessory}
          <Text role="heading" aria-level={1} style={styles.heading}>
            {title}
          </Text>
          {children}
        </View>
      </ScrollView>
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    root: {
      flex: 1,
      backgroundColor: theme.colors.background,
    },
    scroll: {
      flex: 1,
    },
    scrollContent: {
      flexGrow: 1,
      alignItems: 'center' as const,
    },
    content: {
      width: '100%' as const,
      maxWidth: CONTENT_MAX_WIDTH,
      padding: theme.spacing.xl,
      gap: theme.spacing.lg,
    },
    heading: {
      ...theme.typography.heading,
      color: theme.colors.text,
    },
  };
}

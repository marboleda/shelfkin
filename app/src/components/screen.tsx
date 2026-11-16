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
  /**
   * Shown as the level-1 heading instead of `title` (which still names the browser
   * tab), e.g. the household switcher on Pantry.
   */
  header?: ReactNode;
  /**
   * False for a screen whose content scrolls itself, such as a virtualised list
   * (a `FlatList` inside a `ScrollView` renders every row). The content then
   * fills the screen's height instead of scrolling as a whole.
   */
  scroll?: boolean;
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

export function Screen({ title, inShell = false, accessory, header, scroll = true, children }: ScreenProps) {
  const styles = useThemedStyles(createStyles);
  const insets = useSafeAreaInsets();
  const wide = useIsWideLayout();

  const content = (
    <View testID="screen-content" style={[styles.content, scroll ? null : styles.contentFill]}>
      {accessory}
      {header ? (
        <View role="heading" aria-level={1}>
          {header}
        </View>
      ) : (
        <Text role="heading" aria-level={1} style={styles.heading}>
          {title}
        </Text>
      )}
      {children}
    </View>
  );

  return (
    <View testID="screen" style={[styles.root, safeAreaPadding(insets, inShell, wide)]}>
      <Head>
        <title>{documentTitle(title)}</title>
      </Head>
      {scroll ? (
        <ScrollView style={styles.scroll} contentContainerStyle={styles.scrollContent}>
          {content}
        </ScrollView>
      ) : (
        <View style={styles.fixed}>{content}</View>
      )}
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
    fixed: {
      flex: 1,
      alignItems: 'center' as const,
    },
    contentFill: {
      flex: 1,
      paddingBottom: 0,
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

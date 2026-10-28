import type { BottomTabBarProps } from 'expo-router/js-tabs';
import type { Href } from 'expo-router';
import { Text, View } from 'react-native';

import { useIsWideLayout } from '@/hooks/use-is-wide-layout';
import { SIDE_NAV_WIDTH, useTheme, useThemedStyles, type Theme } from '@/theme';

import { PressableLink } from './pressable-link';
import { APP_NAME } from './screen';

type NavItem = {
  label: string;
  href: Href;
  /** Route names (in the `(app)` tabs layout) that count as this section. */
  routes: string[];
};

export const NAV_ITEMS: NavItem[] = [
  { label: 'Pantry', href: '/', routes: ['index', 'add', 'entries/[id]'] },
  { label: 'Settings', href: '/settings', routes: ['settings'] },
];

/**
 * Tab bar renderer for the `(app)` shell: a bottom tab bar on narrow windows and a
 * left side nav on wide ones. Both read the focused route from the tab navigator.
 */
export function AppNav({ state, insets }: BottomTabBarProps) {
  const wide = useIsWideLayout();
  const focusedRoute = state.routes[state.index]?.name;
  const activeLabel = NAV_ITEMS.find((item) => focusedRoute && item.routes.includes(focusedRoute))?.label;

  return wide ? (
    <SideNav activeLabel={activeLabel} insets={insets} />
  ) : (
    <BottomTabBar activeLabel={activeLabel} insets={insets} />
  );
}

type NavProps = {
  activeLabel: string | undefined;
  insets: BottomTabBarProps['insets'];
};

export function BottomTabBar({ activeLabel, insets }: NavProps) {
  const styles = useThemedStyles(createStyles);
  return (
    <View
      testID="bottom-tab-bar"
      role="tablist"
      style={[
        styles.tabBar,
        {
          paddingBottom: insets.bottom,
          paddingLeft: insets.left,
          paddingRight: insets.right,
        },
      ]}
    >
      {NAV_ITEMS.map((item) => {
        const active = item.label === activeLabel;
        return (
          <PressableLink
            key={item.label}
            href={item.href}
            role="tab"
            aria-selected={active}
            accessibilityState={{ selected: active }}
            style={styles.tabItem}
          >
            <View style={[styles.tabIndicator, active && styles.tabIndicatorActive]} />
            <Text style={[styles.navLabel, active && styles.navLabelActive]}>{item.label}</Text>
          </PressableLink>
        );
      })}
    </View>
  );
}

export function SideNav({ activeLabel, insets }: NavProps) {
  const styles = useThemedStyles(createStyles);
  const { spacing } = useTheme();
  return (
    <View
      testID="side-nav"
      role="navigation"
      style={[
        styles.sideNav,
        {
          paddingTop: insets.top + spacing.xl,
          paddingBottom: insets.bottom + spacing.xl,
          paddingLeft: insets.left + spacing.md,
        },
      ]}
    >
      <Text style={styles.brand}>{APP_NAME}</Text>
      {NAV_ITEMS.map((item) => {
        const active = item.label === activeLabel;
        return (
          <PressableLink
            key={item.label}
            href={item.href}
            role="tab"
            aria-selected={active}
            accessibilityState={{ selected: active }}
            style={[styles.sideItem, active && styles.sideItemActive]}
          >
            <Text style={[styles.navLabel, active && styles.navLabelActive]}>{item.label}</Text>
          </PressableLink>
        );
      })}
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    tabBar: {
      flexDirection: 'row' as const,
      backgroundColor: theme.colors.surface,
      borderTopWidth: 1,
      borderTopColor: theme.colors.border,
    },
    tabItem: {
      flex: 1,
      alignItems: 'center' as const,
      paddingBottom: theme.spacing.md,
      minHeight: 52,
    },
    tabIndicator: {
      alignSelf: 'stretch' as const,
      height: 3,
      marginBottom: theme.spacing.sm,
      marginHorizontal: theme.spacing.xl,
      borderBottomLeftRadius: theme.radii.sm,
      borderBottomRightRadius: theme.radii.sm,
      backgroundColor: 'transparent',
    },
    tabIndicatorActive: {
      backgroundColor: theme.colors.primary,
    },
    sideNav: {
      width: SIDE_NAV_WIDTH,
      paddingRight: theme.spacing.md,
      gap: theme.spacing.xs,
      backgroundColor: theme.colors.surface,
      borderRightWidth: 1,
      borderRightColor: theme.colors.border,
    },
    brand: {
      ...theme.typography.title,
      color: theme.colors.text,
      paddingHorizontal: theme.spacing.md,
      marginBottom: theme.spacing.lg,
    },
    sideItem: {
      paddingVertical: theme.spacing.sm,
      paddingHorizontal: theme.spacing.md,
      borderRadius: theme.radii.md,
    },
    sideItemActive: {
      backgroundColor: theme.colors.primarySubtle,
    },
    navLabel: {
      ...theme.typography.label,
      color: theme.colors.mutedText,
    },
    navLabelActive: {
      color: theme.colors.primary,
    },
  };
}

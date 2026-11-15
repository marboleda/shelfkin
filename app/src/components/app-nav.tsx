import Feather from '@expo/vector-icons/Feather';
import type { BottomTabBarProps } from 'expo-router/js-tabs';
import type { Href } from 'expo-router';
import type { ComponentProps } from 'react';
import { Text, View } from 'react-native';

import { useIsWideLayout } from '@/hooks/use-is-wide-layout';
import { MIN_TOUCH_TARGET, SIDE_NAV_WIDTH, useTheme, useThemedStyles, type Theme } from '@/theme';

import { HouseholdSwitcher } from './household-switcher';
import { PressableLink } from './pressable-link';
import { APP_NAME } from './screen';

type NavItem = {
  label: string;
  href: Href;
  /** Feather glyph shown above the label in the bottom tab bar. */
  icon: ComponentProps<typeof Feather>['name'];
  /** Route names (in the `(app)` tabs layout) that count as this section. */
  routes: string[];
};

export const NAV_ITEMS: NavItem[] = [
  { label: 'Pantry', href: '/', icon: 'package', routes: ['index', 'add', 'entries/[id]'] },
  { label: 'Settings', href: '/settings', icon: 'settings', routes: ['settings'] },
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

/** Size of the tab bar icons, in px / dp. */
const TAB_ICON_SIZE = 22;

export function BottomTabBar({ activeLabel, insets }: NavProps) {
  const styles = useThemedStyles(createStyles);
  const { colors } = useTheme();
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
            aria-label={item.label}
            style={styles.tabItem}
          >
            <View
              testID={`tab-icon-pill-${item.label}`}
              style={[styles.tabIconPill, active && styles.tabIconPillActive]}
            >
              {/* Decorative: the tab is announced by its label alone. */}
              <Feather
                testID={`tab-icon-${item.label}`}
                name={item.icon}
                size={TAB_ICON_SIZE}
                color={active ? colors.primary : colors.mutedText}
                aria-hidden
                accessible={false}
                importantForAccessibility="no-hide-descendants"
              />
            </View>
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
      <HouseholdSwitcher placement="sideNav" />
      {NAV_ITEMS.map((item) => {
        const active = item.label === activeLabel;
        return (
          <PressableLink
            key={item.label}
            href={item.href}
            role="tab"
            aria-selected={active}
            accessibilityState={{ selected: active }}
            aria-label={item.label}
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
      justifyContent: 'center' as const,
      gap: theme.spacing.xs,
      paddingTop: theme.spacing.sm,
      paddingBottom: theme.spacing.sm,
      // At least a 44 x 44 touch target.
      minWidth: MIN_TOUCH_TARGET,
      minHeight: MIN_TOUCH_TARGET,
    },
    tabIconPill: {
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      paddingHorizontal: theme.spacing.lg,
      paddingVertical: theme.spacing.xs,
      borderRadius: theme.radii.pill,
    },
    tabIconPillActive: {
      backgroundColor: theme.colors.primarySubtle,
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
      minHeight: MIN_TOUCH_TARGET,
      justifyContent: 'center' as const,
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

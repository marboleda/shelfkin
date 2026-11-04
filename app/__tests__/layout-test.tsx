import { act, fireEvent, within } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { Dimensions, StyleSheet } from 'react-native';

import { colors, radii } from '@/theme';
import { isWideLayout, WIDE_LAYOUT_BREAKPOINT } from '@/theme/layout';
import { setupApiServer } from '../test-utils/api';
import { signedInApiHandlers, signInTestUser } from '../test-utils/auth';

jest.setTimeout(60000);

// These tests render the signed-in app: sign the test user in and answer its API calls.
setupApiServer(...signedInApiHandlers());
beforeEach(() => {
  signInTestUser();
});

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

const APP_DIR = './src/app';

async function setWindowWidth(width: number) {
  const metrics = { width, height: 900, scale: 1, fontScale: 1 };
  await act(async () => {
    Dimensions.set({ window: metrics, screen: metrics });
  });
}

function selectedNavLabel(navTestID: string) {
  const nav = screen.getByTestId(navTestID);
  return within(nav)
    .getAllByRole('tab')
    .filter((tab) => tab.props.accessibilityState?.selected)
    .map((tab) => within(tab).getByText(/.+/).props.children);
}

describe('breakpoint', () => {
  test('the breakpoint is 768 px', () => {
    expect(WIDE_LAYOUT_BREAKPOINT).toBe(768);
    expect(isWideLayout(767)).toBe(false);
    expect(isWideLayout(768)).toBe(true);
  });
});

describe('phone and desktop layout', () => {
  test.each([320, 390, 767])(
    'below 768 px (%i px) shows the bottom tab bar and no side nav',
    async (width) => {
      await setWindowWidth(width);
      await renderRouter(APP_DIR, { initialUrl: '/' });

      expect(screen.getByTestId('bottom-tab-bar')).toBeTruthy();
      expect(screen.queryByTestId('side-nav')).toBeNull();
    },
  );

  test.each([768, 1024, 1440])('at %i px shows the side nav and no bottom tab bar', async (width) => {
    await setWindowWidth(width);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(screen.getByTestId('side-nav')).toBeTruthy();
    expect(screen.queryByTestId('bottom-tab-bar')).toBeNull();
  });

  test('the tab bar highlights the current screen and switches between Pantry and Settings', async () => {
    await setWindowWidth(390);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(selectedNavLabel('bottom-tab-bar')).toEqual(['Pantry']);

    await act(async () => {
      fireEvent.press(within(screen.getByTestId('bottom-tab-bar')).getByText('Settings'));
    });
    expect(screen).toHavePathname('/settings');
    expect(screen.getByRole('heading', { name: 'Settings' })).toBeTruthy();
    expect(selectedNavLabel('bottom-tab-bar')).toEqual(['Settings']);
  });

  test('the side nav highlights the current screen', async () => {
    await setWindowWidth(1024);
    await renderRouter(APP_DIR, { initialUrl: '/settings' });

    expect(selectedNavLabel('side-nav')).toEqual(['Settings']);
  });

  test.each(['/', '/add', '/entries/abc'])(
    'on %s the Pantry tab is selected, with its icon in a primarySubtle pill',
    async (url) => {
      await setWindowWidth(390);
      await renderRouter(APP_DIR, { initialUrl: url });

      expect(selectedNavLabel('bottom-tab-bar')).toEqual(['Pantry']);
      expect(screen.getByTestId('tab-icon-pill-Pantry')).toHaveStyle({
        backgroundColor: colors.light.primarySubtle,
        borderRadius: radii.pill,
      });
      expect(screen.getByTestId('tab-icon-pill-Settings')).not.toHaveStyle({
        backgroundColor: colors.light.primarySubtle,
      });
      // Icons are hidden from screen readers, so include hidden elements to find them.
      const hidden = { includeHiddenElements: true };
      expect(screen.getByTestId('tab-icon-Pantry', hidden)).toHaveStyle({ color: colors.light.primary });
      expect(screen.getByTestId('tab-icon-Settings', hidden)).toHaveStyle({ color: colors.light.mutedText });

      const tabBar = within(screen.getByTestId('bottom-tab-bar'));
      expect(StyleSheet.flatten(tabBar.getByText('Pantry').props.style).color).toBe(colors.light.primary);
      expect(StyleSheet.flatten(tabBar.getByText('Settings').props.style).color).toBe(
        colors.light.mutedText,
      );
    },
  );

  test('each tab has an icon and a label, is read by its label, and is at least 44 x 44', async () => {
    await setWindowWidth(390);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    const tabs = within(screen.getByTestId('bottom-tab-bar')).getAllByRole('tab');
    expect(tabs.map((tab) => tab.props.accessibilityLabel)).toEqual(['Pantry', 'Settings']);
    expect(screen.getByRole('tab', { name: 'Pantry', selected: true })).toBeTruthy();
    for (const tab of tabs) {
      const style = StyleSheet.flatten(tab.props.style);
      expect(style.minWidth).toBeGreaterThanOrEqual(44);
      expect(style.minHeight).toBeGreaterThanOrEqual(44);
    }
    for (const label of ['Pantry', 'Settings']) {
      const icon = screen.getByTestId(`tab-icon-${label}`, { includeHiddenElements: true });
      // Decorative: hidden from screen readers on every platform.
      expect(icon).toHaveProp('aria-hidden', true);
      expect(icon).toHaveProp('accessible', false);
      expect(icon).toHaveProp('importantForAccessibility', 'no-hide-descendants');
    }
  });

  test('the side nav shows the Shelfkin name and marks the active item', async () => {
    await setWindowWidth(1024);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    const sideNav = within(screen.getByTestId('side-nav'));
    const brand = sideNav.getByText('Shelfkin');
    expect(StyleSheet.flatten(brand.props.style).fontFamily).toBe('Figtree_700Bold');
    expect(selectedNavLabel('side-nav')).toEqual(['Pantry']);

    const [pantry, settings] = sideNav.getAllByRole('tab');
    expect(pantry).toHaveStyle({ backgroundColor: colors.light.primarySubtle, borderRadius: 10 });
    expect(settings).not.toHaveStyle({ backgroundColor: colors.light.primarySubtle });
    expect(StyleSheet.flatten(sideNav.getByText('Pantry').props.style).color).toBe(colors.light.primary);
    expect(StyleSheet.flatten(sideNav.getByText('Settings').props.style).color).toBe(
      colors.light.mutedText,
    );
  });

  test('resizing across 768 px switches layouts without leaving the current screen', async () => {
    await setWindowWidth(390);
    await renderRouter(APP_DIR, { initialUrl: '/entries/abc' });
    expect(screen.getByTestId('bottom-tab-bar')).toBeTruthy();

    await setWindowWidth(1024);
    expect(screen.getByTestId('side-nav')).toBeTruthy();
    expect(screen.queryByTestId('bottom-tab-bar')).toBeNull();
    expect(screen).toHavePathname('/entries/abc');
    expect(screen.getByTestId('entry-id')).toHaveTextContent('abc');

    await setWindowWidth(600);
    expect(screen.getByTestId('bottom-tab-bar')).toBeTruthy();
    expect(screen.queryByTestId('side-nav')).toBeNull();
    expect(screen).toHavePathname('/entries/abc');
  });
});

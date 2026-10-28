import { act, fireEvent, within } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { Dimensions } from 'react-native';

import { isWideLayout, WIDE_LAYOUT_BREAKPOINT } from '@/theme/layout';

jest.setTimeout(60000);

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
  test.each([320, 390, 767])('below 768 px (%i px) shows the bottom tab bar and no side nav', async (width) => {
    await setWindowWidth(width);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(screen.getByTestId('bottom-tab-bar')).toBeTruthy();
    expect(screen.queryByTestId('side-nav')).toBeNull();
  });

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

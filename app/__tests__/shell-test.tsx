import { act } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { Dimensions, StyleSheet, useColorScheme } from 'react-native';

import { documentTitle, safeAreaPadding } from '@/components/screen';
import { colors, CONTENT_MAX_WIDTH } from '@/theme';

jest.setTimeout(60000);

jest.mock('react-native/Libraries/Utilities/useColorScheme', () => ({
  __esModule: true,
  default: jest.fn(() => 'light'),
}));

const mockUseColorScheme = useColorScheme as jest.MockedFunction<typeof useColorScheme>;

const APP_DIR = './src/app';

async function setWindowWidth(width: number) {
  const metrics = { width, height: 900, scale: 1, fontScale: 1 };
  await act(async () => {
    Dimensions.set({ window: metrics, screen: metrics });
  });
}

const ROUTES = [
  ['/', 'Pantry'],
  ['/add', 'Add item'],
  ['/entries/abc', 'Entry detail'],
  ['/settings', 'Settings'],
  ['/sign-in', 'Sign in'],
  ['/onboarding', 'Onboarding'],
  ['/nope', 'Page not found'],
] as const;

afterEach(async () => {
  mockUseColorScheme.mockReturnValue('light');
  await setWindowWidth(390);
});

describe('browser tab title', () => {
  test('names the current screen', () => {
    expect(documentTitle('Settings')).toBe('Settings · Shelfkin');
  });
});

describe('dark mode', () => {
  test.each(ROUTES)('%s (%s) has a dark background and light heading', async (url, heading) => {
    mockUseColorScheme.mockReturnValue('dark');
    await renderRouter(APP_DIR, { initialUrl: url });

    expect(screen.getByTestId('screen')).toHaveStyle({ backgroundColor: colors.dark.background });
    const headingEl = screen.getByRole('heading', { name: heading });
    expect(StyleSheet.flatten(headingEl.props.style).color).toBe(colors.dark.text);
  });

  test('the side nav is dark in dark mode', async () => {
    mockUseColorScheme.mockReturnValue('dark');
    await setWindowWidth(1024);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(screen.getByTestId('side-nav')).toHaveStyle({ backgroundColor: colors.dark.surface });
  });
});

describe('wide layout', () => {
  test('at 1440 px, screen content is limited to a readable max width', async () => {
    await setWindowWidth(1440);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(CONTENT_MAX_WIDTH).toBeLessThan(1440);
    expect(screen.getByTestId('screen-content')).toHaveStyle({ maxWidth: CONTENT_MAX_WIDTH });
  });
});

describe('safe areas', () => {
  const insets = { top: 47, right: 0, bottom: 34, left: 0 };

  test('screens outside the shell pad every edge', () => {
    expect(safeAreaPadding(insets, false, false)).toEqual({
      paddingTop: 47,
      paddingRight: 0,
      paddingBottom: 34,
      paddingLeft: 0,
    });
  });

  test('on phones, the bottom tab bar owns the bottom inset and the screen pads the top', () => {
    const padding = safeAreaPadding(insets, true, false);
    expect(padding.paddingTop).toBe(47);
    expect(padding).not.toHaveProperty('paddingBottom');
  });

  test('on wide layouts, the side nav owns the left inset', () => {
    const padding = safeAreaPadding({ ...insets, left: 44 }, true, true);
    expect(padding.paddingTop).toBe(47);
    expect(padding.paddingBottom).toBe(34);
    expect(padding).not.toHaveProperty('paddingLeft');
  });
});

// The app has no Node type definitions, so type the few fs calls used here by hand.
const fs: {
  readdirSync(path: string): string[];
  readFileSync(path: string, encoding: 'utf8'): string;
  statSync(path: string): { isDirectory(): boolean };
} = jest.requireActual('fs');

describe('no hard-coded colours', () => {
  // Jest runs with the app/ directory as its working directory.
  function sourceFiles(dir: string): string[] {
    return fs.readdirSync(dir).flatMap((name) => {
      const path = `${dir}/${name}`;
      return fs.statSync(path).isDirectory() ? sourceFiles(path) : /\.tsx?$/.test(name) ? [path] : [];
    });
  }

  const files = [...sourceFiles('src/app'), ...sourceFiles('src/components')];

  test('finds the route and component sources', () => {
    expect(files).toContain('src/app/(app)/index.tsx');
    expect(files).toContain('src/components/app-nav.tsx');
  });

  test.each(files)('%s reads colours from the theme', (file) => {
    const source = fs.readFileSync(file, 'utf8');
    expect(source).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(source).not.toMatch(/rgba?\(|hsla?\(/);
    expect(source).not.toMatch(/['"](white|black|transparent)['"]/);
  });
});

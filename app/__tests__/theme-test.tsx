import { renderHook } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { StyleSheet, useColorScheme } from 'react-native';

import { colors, fontFaces, getTheme, radii, spacing, typography, useTheme } from '@/theme';
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

jest.mock('react-native/Libraries/Utilities/useColorScheme', () => ({
  __esModule: true,
  default: jest.fn(() => 'light'),
}));

const mockUseColorScheme = useColorScheme as jest.MockedFunction<typeof useColorScheme>;

/** WCAG 2.x relative luminance of a #RRGGBB colour. */
function luminance(hex: string) {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

afterEach(() => {
  mockUseColorScheme.mockReturnValue('light');
});

describe('theme', () => {
  test('returns dark colours when the colour scheme is dark', async () => {
    mockUseColorScheme.mockReturnValue('dark');
    const { result } = await renderHook(() => useTheme());

    expect(result.current.dark).toBe(true);
    expect(result.current.colors).toEqual(colors.dark);
    expect(luminance(result.current.colors.background)).toBeLessThan(0.05);
    expect(luminance(result.current.colors.text)).toBeGreaterThan(0.7);
  });

  test('returns light colours for light or unknown schemes', () => {
    expect(getTheme('light').colors).toEqual(colors.light);
    expect(getTheme(null).colors).toEqual(colors.light);
    expect(getTheme(undefined).dark).toBe(false);
  });

  test('updates when the OS switches between light and dark', async () => {
    const { result, rerender } = await renderHook(() => useTheme());
    expect(result.current.colors.background).toBe(colors.light.background);

    mockUseColorScheme.mockReturnValue('dark');
    await rerender({});
    expect(result.current.colors.background).toBe(colors.dark.background);
  });

  test('has exactly the designed colour tokens, in both modes', () => {
    expect(colors.light).toEqual({
      background: '#F7F6F2',
      surface: '#FFFFFF',
      text: '#1C1B19',
      mutedText: '#5F5C55',
      border: '#DDD9CF',
      primary: '#2F6B4F',
      onPrimary: '#FFFFFF',
      primarySubtle: '#E3EFE8',
      danger: '#B3261E',
      dangerSubtle: '#F9E1DE',
      expiringSoon: '#8A4B00',
      expiringSubtle: '#FBEBD9',
      levelLow: '#C27A12',
      levelEmpty: '#E4E0D6',
    });
    expect(colors.dark).toEqual({
      background: '#141513',
      surface: '#1E201D',
      text: '#ECEAE4',
      mutedText: '#A6A39B',
      border: '#363830',
      primary: '#7BC5A0',
      onPrimary: '#0F2A1D',
      primarySubtle: '#23382C',
      danger: '#F2B8B5',
      dangerSubtle: '#3A1F1D',
      expiringSoon: '#F5B74A',
      expiringSubtle: '#3A2A14',
      levelLow: '#E0A04A',
      levelEmpty: '#3A3C35',
    });
  });

  test('the splash screen background in app.json matches the theme background', () => {
    const appJson = jest.requireActual('../app.json');
    const splash = appJson.expo.plugins.find(
      (plugin: unknown) => Array.isArray(plugin) && plugin[0] === 'expo-splash-screen',
    )[1];
    expect(splash.backgroundColor).toBe(colors.light.background);
    expect(splash.dark.backgroundColor).toBe(colors.dark.background);
  });

  test('the web page paints the theme background before the JS bundle runs', () => {
    // The default `web.output: "single"` builds every page from public/index.html.
    const fs: { readFileSync(path: string, encoding: 'utf8'): string } = jest.requireActual('fs');
    const html = fs.readFileSync('public/index.html', 'utf8');
    const style = html.match(/<style id="theme-background">([\s\S]*?)<\/style>/)?.[1] ?? '';
    const [light, dark] = style.split('@media (prefers-color-scheme: dark)');

    expect(style).toMatch(/color-scheme:\s*light dark/);
    expect(light).toMatch(new RegExp(`html,\\s*body\\s*\\{\\s*background-color:\\s*${colors.light.background};`));
    expect(dark).toMatch(new RegExp(`html,\\s*body\\s*\\{\\s*background-color:\\s*${colors.dark.background};`));
    // Expo's default head content is kept.
    expect(html).toContain('<style id="expo-reset">');
    expect(html).toContain('name="viewport"');
    expect(html).toContain('<div id="root"></div>');
  });
});

// [foreground, background] pairs used for text: each must meet 4.5:1.
// `levelLow` and `levelEmpty` are graphics only and are deliberately not listed.
const TEXT_PAIRS = [
  ['text', 'background'],
  ['text', 'surface'],
  ['mutedText', 'background'],
  ['mutedText', 'surface'],
  ['primary', 'background'],
  ['primary', 'surface'],
  ['primary', 'primarySubtle'],
  ['mutedText', 'primarySubtle'],
  ['onPrimary', 'primary'],
  ['danger', 'background'],
  ['danger', 'surface'],
  ['danger', 'dangerSubtle'],
  ['expiringSoon', 'background'],
  ['expiringSoon', 'surface'],
  ['expiringSoon', 'expiringSubtle'],
] as const;

describe.each(['light', 'dark'] as const)('contrast in %s mode', (scheme) => {
  test.each(TEXT_PAIRS)('%s on %s is at least 4.5:1', (fg, bg) => {
    const c = colors[scheme];
    expect(contrast(c[fg], c[bg])).toBeGreaterThanOrEqual(4.5);
  });
});

describe('type, radii and spacing', () => {
  test('the type scale matches the designs', () => {
    const scale = Object.fromEntries(
      Object.entries(typography).map(([name, style]) => [
        name,
        [style.fontSize, style.lineHeight, style.fontWeight],
      ]),
    );
    expect(scale).toEqual({
      heading: [28, 34, '700'],
      title: [20, 26, '700'],
      body: [16, 24, '400'],
      label: [15, 20, '600'],
      caption: [13, 18, '400'],
      overline: [13, 18, '700'],
    });
    expect(typography.overline).toMatchObject({ textTransform: 'uppercase', letterSpacing: 0.6 });
  });

  test.each(Object.keys(typography) as (keyof typeof typography)[])(
    '%s names the Figtree face for its weight',
    (name) => {
      const style = typography[name];
      // On web the face is followed by a system-font fallback stack.
      const face = style.fontFamily.split(',')[0].trim();
      expect(face).toBe(fontFaces[style.fontWeight]);
      expect(face).toMatch(new RegExp(`^Figtree_${style.fontWeight}[A-Za-z]+$`));
    },
  );

  test('heading, title and overline use the 700 face; body the 400 and label the 600 face', () => {
    for (const name of ['heading', 'title', 'overline'] as const) {
      expect(typography[name].fontFamily).toBe('Figtree_700Bold');
    }
    expect(typography.body.fontFamily).toBe('Figtree_400Regular');
    expect(typography.caption.fontFamily).toBe('Figtree_400Regular');
    expect(typography.label.fontFamily).toBe('Figtree_600SemiBold');
  });

  test('radii include card and spacing is unchanged', () => {
    expect(radii).toEqual({ sm: 6, md: 10, card: 12, lg: 16, pill: 999 });
    expect(spacing).toEqual({ xs: 4, sm: 8, md: 12, lg: 16, xl: 24, xxl: 32 });
  });
});

describe('theme on screen', () => {

  test('in dark mode the screen and heading use dark background and light text', async () => {
    mockUseColorScheme.mockReturnValue('dark');
    await renderRouter('./src/app', { initialUrl: '/' });

    const heading = screen.getByRole('heading', { name: 'Pantry' });
    expect(StyleSheet.flatten(heading.props.style).color).toBe(colors.dark.text);
    expect(StyleSheet.flatten(heading.props.style).fontFamily).toBe('Figtree_700Bold');
    expect(screen.getByTestId('bottom-tab-bar')).toHaveStyle({ backgroundColor: colors.dark.surface });
    // Themed styles must survive <Link asChild> (a style function on the child is dropped).
    expect(screen.getByTestId('add-item-button')).toHaveStyle({ backgroundColor: colors.dark.primary });
  });
});

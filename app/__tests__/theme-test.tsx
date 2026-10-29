import { renderHook } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { StyleSheet, useColorScheme } from 'react-native';

import { colors, getTheme, useTheme } from '@/theme';

jest.setTimeout(60000);

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

  test('defines every starter colour token in both modes', () => {
    const keys = [
      'background',
      'surface',
      'text',
      'mutedText',
      'border',
      'primary',
      'danger',
      'expiringSoon',
    ];
    for (const scheme of ['light', 'dark'] as const) {
      for (const key of keys) {
        expect(colors[scheme]).toHaveProperty(key, expect.stringMatching(/^#[0-9A-F]{6}$/i));
      }
    }
  });

  test.each(['light', 'dark'] as const)('body and muted text meet 4.5:1 on the background (%s)', (scheme) => {
    const c = colors[scheme];
    expect(contrast(c.text, c.background)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(c.mutedText, c.background)).toBeGreaterThanOrEqual(4.5);
    // Nav labels and rows sit on `surface`.
    expect(contrast(c.text, c.surface)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(c.mutedText, c.surface)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(c.primary, c.primarySubtle)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(c.onPrimary, c.primary)).toBeGreaterThanOrEqual(4.5);
  });

  test('in dark mode the screen and heading use dark background and light text', async () => {
    mockUseColorScheme.mockReturnValue('dark');
    await renderRouter('./src/app', { initialUrl: '/' });

    const heading = screen.getByRole('heading', { name: 'Pantry' });
    expect(StyleSheet.flatten(heading.props.style).color).toBe(colors.dark.text);
    expect(screen.getByTestId('bottom-tab-bar')).toHaveStyle({ backgroundColor: colors.dark.surface });
    // Themed styles must survive <Link asChild> (a style function on the child is dropped).
    expect(screen.getByTestId('add-item-button')).toHaveStyle({ backgroundColor: colors.dark.primary });
  });
});

import { act } from '@testing-library/react-native';
import { useFonts } from 'expo-font';
import { renderRouter, screen } from 'expo-router/testing-library';
import * as SplashScreen from 'expo-splash-screen';
import { useColorScheme } from 'react-native';

import { colors } from '@/theme';
import { FONT_LOAD_TIMEOUT_MS, fontSources } from '@/theme/fonts';

jest.setTimeout(60000);

// Fonts never really load in Jest. Each test decides what `useFonts` reports.
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: jest.fn(() => [true, null]),
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

jest.mock('expo-splash-screen', () => ({
  preventAutoHideAsync: jest.fn(() => Promise.resolve(true)),
  hide: jest.fn(),
  hideAsync: jest.fn(() => Promise.resolve()),
}));

jest.mock('react-native/Libraries/Utilities/useColorScheme', () => ({
  __esModule: true,
  default: jest.fn(() => 'light'),
}));

const mockUseFonts = useFonts as jest.MockedFunction<typeof useFonts>;
const mockUseColorScheme = useColorScheme as jest.MockedFunction<typeof useColorScheme>;
const mockHide = SplashScreen.hide as jest.MockedFunction<typeof SplashScreen.hide>;

const APP_DIR = './src/app';

beforeEach(() => {
  mockHide.mockClear();
});

afterEach(() => {
  mockUseFonts.mockImplementation(() => [true, null]);
  mockUseColorScheme.mockReturnValue('light');
});

describe('font loading', () => {
  test('loads only the Figtree weights the type scale uses, plus the nav icon font', () => {
    const families = Object.keys(fontSources);
    expect(families.filter((name) => name.startsWith('Figtree')).sort()).toEqual([
      'Figtree_400Regular',
      'Figtree_600SemiBold',
      'Figtree_700Bold',
    ]);
    expect(families).toContain('feather');
  });

  test('keeps the splash screen up, on a themed background, while fonts are loading', async () => {
    mockUseFonts.mockImplementation(() => [false, null]);
    mockUseColorScheme.mockReturnValue('dark');
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(SplashScreen.preventAutoHideAsync).toHaveBeenCalled();
    expect(mockHide).not.toHaveBeenCalled();
    // No white panel behind the splash screen (or on web, which has none).
    expect(screen.getByTestId('fonts-loading')).toHaveStyle({ backgroundColor: colors.dark.background });
    expect(screen.queryByRole('heading', { name: 'Pantry' })).toBeNull();
  });

  test('opens the app and hides the splash screen once fonts load', async () => {
    mockUseFonts.mockImplementation(() => [true, null]);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(screen.queryByTestId('fonts-loading')).toBeNull();
    expect(screen.getByRole('heading', { name: 'Pantry' })).toBeTruthy();
    expect(mockHide).toHaveBeenCalled();
  });

  test('if fonts fail to load, the app still opens in the system font', async () => {
    const warn = jest.spyOn(console, 'warn').mockImplementation(() => {});
    mockUseFonts.mockImplementation(() => [false, new Error('offline')]);
    await renderRouter(APP_DIR, { initialUrl: '/' });

    expect(screen.getByRole('heading', { name: 'Pantry' })).toBeTruthy();
    expect(screen.getByTestId('bottom-tab-bar')).toBeTruthy();
    expect(mockHide).toHaveBeenCalled();
    warn.mockRestore();
  });

  test('if fonts never settle, the app opens after the timeout instead of waiting forever', async () => {
    mockUseFonts.mockImplementation(() => [false, null]);
    await renderRouter(APP_DIR, { initialUrl: '/' });
    expect(screen.queryByRole('heading', { name: 'Pantry' })).toBeNull();

    await act(async () => {
      jest.advanceTimersByTime(FONT_LOAD_TIMEOUT_MS);
    });

    expect(screen.getByRole('heading', { name: 'Pantry' })).toBeTruthy();
    expect(mockHide).toHaveBeenCalled();
  });
});

// Focus around the household switcher's overlays on web.
//
// On web, RN-Web's Modal traps focus while it is mounted: focusing the switcher
// button while the Modal is still there is undone (focus goes back inside the
// Modal, then to <body> when it unmounts). So focus must go back to the button
// only after the Modal reports `onDismiss`, which RN-Web calls once it has
// unmounted. The trap also moves focus to the first element in the Modal that
// accepts `focus()`, so the backdrop must not accept it.
//
// Jest renders the native tree, so these tests record the order of events:
// the Modal mock below keeps each Modal's latest props, so a test can call
// `onDismiss` the way RN-Web does, after the close.

import { act, fireEvent } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import AsyncStorage from '@react-native-async-storage/async-storage';
import { Dimensions } from 'react-native';

import { focusElement } from '@/components/focus';
import { rememberedHouseholdKey } from '@/household/storage';
import { setupApiServer } from '../test-utils/api';
import { signedInApiHandlers, signInTestUser, testHousehold } from '../test-utils/auth';

jest.setTimeout(120000);

jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

// Behave as on web, where focus waits for the Modal's onDismiss.
jest.mock('@/components/focus', () => ({ focusElement: jest.fn(), focusWaitsForModalDismiss: () => true }));
const mockFocus = focusElement as jest.MockedFunction<typeof focusElement>;

// Each Modal's latest props, by its accessible name.
const mockModalProps = new Map<string, { visible?: boolean; onDismiss?: () => void }>();
jest.mock('react-native/Libraries/Modal/Modal', () => {
  const React = require('react');
  function Modal(props: { visible?: boolean; children?: unknown; 'aria-label'?: string }) {
    mockModalProps.set(props['aria-label'] ?? '', props);
    return props.visible === false ? null : React.createElement('Modal', props, props.children);
  }
  return { __esModule: true, default: Modal };
});

const APP_DIR = './src/app';
const HOME = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000a', name: 'Home', role: 'owner' });
const CABIN = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000b', name: 'Cabin', role: 'member' });

setupApiServer(...signedInApiHandlers(undefined, [HOME, CABIN]));

const label = (name: string) => `Switch household, current: ${name}`;

/** Focus calls with an element (a row's ref is also called with null as it unmounts). */
function focusCalls() {
  return mockFocus.mock.calls.filter(([element]) => element != null);
}

/** The overlay's backdrop (hidden from screen readers by the modal content beside it). */
function backdrop() {
  return screen.getAllByTestId('overlay-backdrop', { includeHiddenElements: true })[0];
}

async function dismiss(modalLabel: string) {
  const props = mockModalProps.get(modalLabel);
  expect(props?.visible).toBe(false);
  await act(async () => props?.onDismiss?.());
}

async function setWindowWidth(width: number) {
  const metrics = { width, height: 900, scale: 1, fontScale: 1 };
  await act(async () => {
    Dimensions.set({ window: metrics, screen: metrics });
  });
}

/** Renders Pantry with Home current. */
async function renderOnWeb(width: number) {
  const { uid } = signInTestUser();
  await AsyncStorage.setItem(rememberedHouseholdKey(uid), HOME.id);
  await setWindowWidth(width);
  await renderRouter(APP_DIR, { initialUrl: '/' });
  const button = await screen.findByLabelText(label('Home'));
  mockFocus.mockClear();
  return button;
}

beforeEach(() => {
  mockModalProps.clear();
});

describe('on web, focus returns to the switcher button only after the overlay is gone', () => {
  test('Escape in the side-nav popover', async () => {
    await renderOnWeb(1024);
    await fireEvent.press(screen.getByLabelText(label('Home')));
    mockFocus.mockClear();

    await fireEvent(screen.getByTestId('household-popover'), 'accessibilityEscape');
    expect(screen.queryByTestId('household-popover')).toBeNull();
    // The Modal is still mounted on web here: focusing now would be undone by its trap.
    expect(focusCalls()).toHaveLength(0);

    await dismiss('Your households');
    expect(focusCalls()).toHaveLength(1);
  });

  test('choosing a household, and clicking outside', async () => {
    await renderOnWeb(1024);
    await fireEvent.press(screen.getByLabelText(label('Home')));
    mockFocus.mockClear();
    await fireEvent.press(screen.getByLabelText('Cabin, Member'));
    expect(focusCalls()).toHaveLength(0);
    await dismiss('Your households');
    expect(focusCalls()).toHaveLength(1);

    await fireEvent.press(screen.getByLabelText(label('Cabin')));
    mockFocus.mockClear();
    await fireEvent(backdrop(), 'responderRelease');
    expect(screen.queryByTestId('household-popover')).toBeNull();
    expect(focusCalls()).toHaveLength(0);
    await dismiss('Your households');
    expect(focusCalls()).toHaveLength(1);
  });

  test('Escape in the create dialog, not when the popover gives way to it', async () => {
    await renderOnWeb(1024);
    await fireEvent.press(screen.getByLabelText(label('Home')));
    await fireEvent.press(screen.getByRole('button', { name: 'Create a household' }));
    mockFocus.mockClear();

    // The popover closing to open the dialog is not a reason to focus the button.
    await dismiss('Your households');
    expect(focusCalls()).toHaveLength(0);

    await fireEvent(screen.getByTestId('household-dialog'), 'accessibilityEscape');
    expect(screen.queryByTestId('household-dialog')).toBeNull();
    expect(focusCalls()).toHaveLength(0);
    await dismiss('Create a household');
    expect(focusCalls()).toHaveLength(1);
  });

  test('Escape in the bottom sheet below 768 px', async () => {
    await renderOnWeb(390);
    await fireEvent.press(screen.getByLabelText(label('Home')));
    mockFocus.mockClear();

    await fireEvent(screen.getByTestId('household-sheet'), 'accessibilityEscape');
    expect(focusCalls()).toHaveLength(0);
    await dismiss('Your households');
    expect(focusCalls()).toHaveLength(1);
  });
});

test('the backdrop cannot take focus, so the focus trap never lands on it', async () => {
  await renderOnWeb(1024);
  await fireEvent.press(screen.getByLabelText(label('Home')));

  // A Pressable would get a tabindex on web, which makes it focusable by script.
  expect(backdrop().props.onClick).toBeUndefined();
  expect(backdrop().props.tabIndex).toBeUndefined();
  expect(backdrop().props.focusable).toBeUndefined();
  expect(backdrop().props.accessible).toBe(false);
});

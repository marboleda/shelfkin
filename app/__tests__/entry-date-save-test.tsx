// Entry detail's typed dates on web (#123): a typed date is saved once, when
// the person has finished (blur, Enter, leaving the screen, hiding the page),
// never after a pause. Jest runs as iOS, so this file makes `EntryDateField`
// use the web save mode and stands in for the web `DateField` (an
// `<input type="date">`, which Jest can't render) with a TextInput that reports
// change, blur and Enter the same way.

import type { QueryClient } from '@tanstack/react-query';
import { act, cleanup, fireEvent, renderHook, waitFor } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { HttpResponse } from 'msw';

import { useDateDraft } from '@/components/use-date-draft';
import { isPlausibleCalendarDate } from '@/pantry/dates';
import { setupApiServer } from '../test-utils/api';
import { signedInApiHandlers, signInTestUser, testHousehold, testMe } from '../test-utils/auth';
import { fakePantryApi, testEntry, testGroup, testPantry } from '../test-utils/pantry';

jest.setTimeout(180000);

jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

const mockCreatedClients: QueryClient[] = [];
jest.mock('@/api/query-client', () => {
  const actual = jest.requireActual('@/api/query-client');
  return {
    ...actual,
    createQueryClient: jest.fn(() => {
      const client = actual.createQueryClient({ retryDelay: 0, gcTime: Infinity });
      mockCreatedClients.push(client);
      return client;
    }),
  };
});

// Save dates the way the web does.
jest.mock('@/components/use-date-draft', () => ({
  ...jest.requireActual('@/components/use-date-draft'),
  DATE_SAVE_MODE: 'commit',
}));

// The web DateField as a TextInput: typing reports the input's value, blur and Enter are passed on.
jest.mock('@/components/date-field', () => {
  const { Text, TextInput, View } = jest.requireActual('react-native');
  return {
    DateField: (props: {
      label: string;
      value: string;
      onChange: (value: string) => void;
      onBlur?: () => void;
      onSubmitEditing?: () => void;
      hint?: string | null;
      testID?: string;
      reserveHintSpace?: boolean;
    }) => (
      <View>
        <TextInput
          testID={props.testID}
          accessibilityLabel={props.label}
          value={props.value}
          onChangeText={props.onChange}
          onBlur={props.onBlur}
          onSubmitEditing={props.onSubmitEditing}
        />
        {props.hint ? (
          <Text testID={`${props.testID}-hint`}>{props.hint}</Text>
        ) : props.reserveHintSpace ? (
          <Text testID={`${props.testID}-hint-space`}>{' '}</Text>
        ) : null}
      </View>
    ),
  };
});

const APP_DIR = './src/app';
const ME = testMe();
const HOME = testHousehold({ id: '0190f3a1-0000-7000-8000-00000000000a', name: 'Home', role: 'owner' });
const server = setupApiServer(...signedInApiHandlers(ME, [HOME]));

const MILK_ENTRY = testEntry({ level: 'full', locationId: 1, purchasedOn: '2026-10-20', expiresOn: '2026-11-03' });
const MILK = testGroup('Milk', [MILK_ENTRY]);
const UNSAVED_HINT = 'Press Enter or leave the field to save';

let api: ReturnType<typeof fakePantryApi>;

beforeEach(() => {
  signInTestUser();
  api = fakePantryApi(testPantry([MILK]), { actor: { id: ME.id, name: ME.displayName } });
  server.use(...api.handlers);
});

function appQueryClient(): QueryClient {
  const client = mockCreatedClients[mockCreatedClients.length - 1];
  if (!client) throw new Error('The app has not created a QueryClient');
  return client;
}

async function waitForSaved() {
  await waitFor(() => expect(appQueryClient().isMutating()).toBe(0));
  await waitFor(() => expect(appQueryClient().isFetching()).toBe(0));
}

async function openEntry() {
  await renderRouter(APP_DIR, { initialUrl: `/entries/${MILK_ENTRY.id}` });
  await screen.findByTestId('entry-detail');
}

async function type(field: string, value: string) {
  await act(async () => {
    fireEvent.changeText(screen.getByTestId(field), value);
  });
}

async function blur(field: string) {
  await act(async () => {
    fireEvent(screen.getByTestId(field), 'blur');
  });
}

async function pressEnter(field: string) {
  await act(async () => {
    fireEvent(screen.getByTestId(field), 'submitEditing');
  });
}

/** Lets `ms` pass (renderRouter turns on fake timers). */
async function pause(ms: number) {
  await act(async () => {
    jest.advanceTimersByTime(ms);
  });
}

function hint(field: string): string | null {
  const element = screen.queryByTestId(`${field}-hint`);
  return element ? String(element.props.children) : null;
}

describe('typed dates (web)', () => {
  test('in-between dates typed with long pauses send nothing until blur, then one PATCH with the last date', async () => {
    await openEntry();
    // The year typed one digit at a time, as <input type="date"> reports it, then the month and day.
    for (const step of ['0002-10-20', '0020-10-20', '0202-10-20', '2026-10-20', '2026-09-20', '2026-09-22']) {
      await type('entry-bought-date', step);
      await pause(2000); // well past the old 800 ms save delay
    }
    expect(api.state.updates).toHaveLength(0);
    expect(screen.getByTestId('entry-bought-date').props.value).toBe('2026-09-22');
    expect(hint('entry-bought-date')).toBe(UNSAVED_HINT);

    await blur('entry-bought-date');
    await waitForSaved();

    expect(api.state.updates).toHaveLength(1);
    expect(api.state.updates[0].body).toEqual({ eventId: expect.any(String), purchasedOn: '2026-09-22' });
    expect(screen.getByTestId('entry-bought-date').props.value).toBe('2026-09-22');
    expect(hint('entry-bought-date')).toBeNull();
  });

  test('Enter saves once, and leaving the field with the saved date sends nothing', async () => {
    await openEntry();
    await type('entry-expires', '2026-11-20');
    await pressEnter('entry-expires');
    await waitForSaved();
    expect(api.state.updates).toHaveLength(1);
    expect(api.state.updates[0].body).toEqual({ eventId: expect.any(String), expiresOn: '2026-11-20' });

    await pressEnter('entry-expires');
    await blur('entry-expires');
    await waitForSaved();
    expect(api.state.updates).toHaveLength(1);
  });

  test('typing the saved date back, an invalid date or an emptied field sends nothing on blur', async () => {
    await openEntry();
    await type('entry-bought-date', '2026-10-25');
    expect(hint('entry-bought-date')).toBe(UNSAVED_HINT);
    await type('entry-bought-date', '2026-10-20');
    expect(hint('entry-bought-date')).toBeNull();
    await blur('entry-bought-date');

    await type('entry-bought-date', '2026-02-30');
    await blur('entry-bought-date');
    expect(hint('entry-bought-date')).toBe('Enter the date you bought it');

    await type('entry-expires', '');
    await blur('entry-expires');
    expect(hint('entry-expires')).toBe('Enter a date, or choose No expiry');

    await pause(2000);
    expect(api.state.updates).toHaveLength(0);
  });

  test('a year outside 2000-2099 is not saved and shows the incomplete hint', async () => {
    await openEntry();
    for (const date of ['0202-10-22', '1999-10-22', '2100-10-22']) {
      await type('entry-bought-date', date);
      expect(hint('entry-bought-date')).toBe('Enter the date you bought it');
      await blur('entry-bought-date');
      await pressEnter('entry-bought-date');
    }
    await type('entry-expires', '1999-12-31');
    expect(hint('entry-expires')).toBe('Enter a date, or choose No expiry');
    await blur('entry-expires');

    await pause(100);
    expect(api.state.updates).toHaveLength(0);
  });

  // Back to Pantry switches tabs, which keeps Entry detail mounted: the date is saved when the screen loses focus.
  test('leaving the screen with an unsaved date saves it once', async () => {
    await openEntry();
    await type('entry-bought-date', '2026-10-24');
    expect(api.state.updates).toHaveLength(0);

    await act(async () => {
      fireEvent.press(screen.getByRole('link', { name: 'Back to Pantry' }));
    });
    await waitFor(() => expect(api.state.updates).toHaveLength(1));
    await waitForSaved();

    expect(api.state.updates).toHaveLength(1);
    expect(api.state.updates[0].body).toEqual({ eventId: expect.any(String), purchasedOn: '2026-10-24' });
  });
});

/** Whether the field has exactly one line under it: a hint, or the space kept for one. */
function hintLines(field: string): number {
  return (screen.queryByTestId(`${field}-hint`) ? 1 : 0) + (screen.queryByTestId(`${field}-hint-space`) ? 1 : 0);
}

describe('typed dates (web), layout', () => {
  test('the line under Bought and Expires is always there, so the unsaved hint never moves what is below', async () => {
    await openEntry();
    expect(hintLines('entry-bought-date')).toBe(1);
    expect(hintLines('entry-expires')).toBe(1);
    await type('entry-bought-date', '2026-10-25');
    expect(hint('entry-bought-date')).toBe(UNSAVED_HINT);
    expect(hintLines('entry-bought-date')).toBe(1);
    await blur('entry-bought-date');
    await waitForSaved();
    expect(hint('entry-bought-date')).toBeNull();
    expect(hintLines('entry-bought-date')).toBe(1);
  });
});

describe('typed dates (web), hiding the page', () => {
  // Jest has no DOM: stand in for `document` and `window`'s events while Entry detail is open.
  const globals = globalThis as { document?: unknown; window?: { addEventListener?: unknown; removeEventListener?: unknown } };
  let fakeDocument: EventTarget & { visibilityState: string };
  let fakeWindowEvents: EventTarget;
  let restore: () => void;

  beforeEach(() => {
    fakeDocument = Object.assign(new EventTarget(), { visibilityState: 'visible' });
    fakeWindowEvents = new EventTarget();
    const saved = {
      document: globals.document,
      add: globals.window?.addEventListener,
      remove: globals.window?.removeEventListener,
    };
    globals.document = fakeDocument;
    if (globals.window) {
      globals.window.addEventListener = fakeWindowEvents.addEventListener.bind(fakeWindowEvents);
      globals.window.removeEventListener = fakeWindowEvents.removeEventListener.bind(fakeWindowEvents);
    }
    restore = () => {
      globals.document = saved.document;
      if (globals.window) {
        globals.window.addEventListener = saved.add;
        globals.window.removeEventListener = saved.remove;
      }
    };
  });

  afterEach(async () => {
    // Unmount while the stand-ins are still there, so the listeners come off them.
    await cleanup();
    restore();
  });

  async function hidePage() {
    await act(async () => {
      fakeDocument.visibilityState = 'hidden';
      fakeDocument.dispatchEvent(new Event('visibilitychange'));
    });
  }

  test('hiding the page sends the unsaved date at once with keepalive and the last token, once', async () => {
    await openEntry();
    await type('entry-bought-date', '2026-10-26');
    const fetchSpy = jest.spyOn(globalThis, 'fetch');
    try {
      await hidePage();
      // Sent before anything asynchronous could run.
      const patches = fetchSpy.mock.calls.filter(([, init]) => init?.method === 'PATCH');
      expect(patches).toHaveLength(1);
      const [url, init] = patches[0];
      expect(String(url)).toMatch(new RegExp(`/households/${HOME.id}/entries/${MILK_ENTRY.id}$`));
      expect(init?.keepalive).toBe(true);
      expect((init?.headers as Record<string, string>).Authorization).toMatch(/^Bearer .+/);
    } finally {
      fetchSpy.mockRestore();
    }
    await waitForSaved();
    expect(api.state.updates).toHaveLength(1);
    expect(api.state.updates[0].body).toEqual({ eventId: expect.any(String), purchasedOn: '2026-10-26' });
    expect(screen.getByTestId('entry-bought-date').props.value).toBe('2026-10-26');

    // Nothing more on pagehide, on return or on blur.
    await act(async () => {
      fakeWindowEvents.dispatchEvent(new Event('pagehide'));
      fakeDocument.visibilityState = 'visible';
      fakeDocument.dispatchEvent(new Event('visibilitychange'));
    });
    await blur('entry-bought-date');
    await waitForSaved();
    expect(api.state.updates).toHaveLength(1);
  });

  test('a keepalive request turned away with a 401 (a stale token) is sent again the usual way, with the same eventId', async () => {
    api.state.answerUpdate = (_request, call) => (call === 1 ? HttpResponse.json({ error: { code: 'unauthorized', message: 'No.' } }, { status: 401 }) : undefined);
    await openEntry();
    await type('entry-bought-date', '2026-10-27');
    await hidePage();
    await waitForSaved();
    expect(api.state.updates).toHaveLength(2);
    expect(api.state.updates[1].body).toEqual(api.state.updates[0].body);
    expect(screen.getByTestId('entry-bought-date').props.value).toBe('2026-10-27');
  });
});

describe('useDateDraft', () => {
  async function setup(value: string, mode: 'change' | 'commit') {
    const onSave = jest.fn();
    const hook = await renderHook(({ saved }: { saved: string }) => useDateDraft(saved, onSave, mode), {
      initialProps: { saved: value },
    });
    return { onSave, hook };
  }

  test('commit mode saves on commit only, once', async () => {
    const { onSave, hook } = await setup('2026-10-20', 'commit');
    await act(() => hook.result.current.onChange('2026-10-21'));
    expect(onSave).not.toHaveBeenCalled();
    expect(hook.result.current.status).toBe('unsaved');

    await act(() => hook.result.current.commit());
    await act(() => hook.result.current.commit());
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith('2026-10-21');
    // The typed date stays shown until the saved date catches up...
    expect(hook.result.current.shown).toBe('2026-10-21');
    expect(hook.result.current.status).toBeNull();
    await hook.rerender({ saved: '2026-10-21' });
    expect(hook.result.current.shown).toBe('2026-10-21');
    // ...and a rollback after that shows the saved date, not the old draft.
    await hook.rerender({ saved: '2026-10-20' });
    expect(hook.result.current.shown).toBe('2026-10-20');
    await act(() => hook.result.current.commit());
    expect(onSave).toHaveBeenCalledTimes(1);
  });

  test('a saved date that changes underneath replaces an unsaved draft, which is then not saved', async () => {
    const { onSave, hook } = await setup('2026-10-20', 'commit');
    await act(() => hook.result.current.onChange('2026-10-21'));
    await hook.rerender({ saved: '2026-10-25' });
    expect(hook.result.current.shown).toBe('2026-10-25');
    await act(() => hook.result.current.commit());
    expect(onSave).not.toHaveBeenCalled();
  });

  test('unmounting saves an unsaved date once', async () => {
    const { onSave, hook } = await setup('2026-10-20', 'commit');
    await act(() => hook.result.current.onChange('2026-10-23'));
    await hook.unmount();
    expect(onSave).toHaveBeenCalledTimes(1);
    expect(onSave).toHaveBeenCalledWith('2026-10-23');
  });

  test('hiding the page saves an unsaved date once', async () => {
    const fakeDocument = Object.assign(new EventTarget(), { visibilityState: 'visible' });
    const fakeWindow = new EventTarget();
    const globals = globalThis as { document?: unknown; window?: unknown };
    const saved = { document: globals.document, window: globals.window };
    globals.document = fakeDocument;
    globals.window = fakeWindow;
    try {
      const { onSave, hook } = await setup('2026-10-20', 'commit');
      await act(() => hook.result.current.onChange('2026-10-23'));
      await act(() => {
        fakeDocument.visibilityState = 'hidden';
        fakeDocument.dispatchEvent(new Event('visibilitychange'));
        fakeWindow.dispatchEvent(new Event('pagehide'));
      });
      expect(onSave).toHaveBeenCalledTimes(1);
      expect(onSave).toHaveBeenCalledWith('2026-10-23', { keepalive: true });
      await hook.unmount();
      expect(onSave).toHaveBeenCalledTimes(1);
    } finally {
      globals.document = saved.document;
      globals.window = saved.window;
    }
  });

  test('change mode (iOS and Android) saves each picked date at once, whatever the year', async () => {
    const { onSave, hook } = await setup('2026-10-20', 'change');
    await act(() => hook.result.current.onChange('2026-10-21'));
    await act(() => hook.result.current.onChange('1999-10-21'));
    expect(onSave.mock.calls).toEqual([['2026-10-21'], ['1999-10-21']]);
    expect(hook.result.current.status).toBeNull();
  });
});

test('isPlausibleCalendarDate accepts valid dates in 2000-2099 only', () => {
  expect(isPlausibleCalendarDate('2000-01-01')).toBe(true);
  expect(isPlausibleCalendarDate('2099-12-31')).toBe(true);
  expect(isPlausibleCalendarDate('2026-02-29')).toBe(false);
  expect(isPlausibleCalendarDate('1999-12-31')).toBe(false);
  expect(isPlausibleCalendarDate('2100-01-01')).toBe(false);
  expect(isPlausibleCalendarDate('0202-10-20')).toBe(false);
  expect(isPlausibleCalendarDate('')).toBe(false);
});

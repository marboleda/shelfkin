import { ApiError } from '@/api/errors';
import { cleanItemName, sameItemName } from '@/catalog/catalog';
import { fromPickerDate, toPickerDate } from '@/components/date-field-props';
import { newId } from '@/lib/ids';
import { withRetries } from '@/lib/retry';
import { addDays, isValidCalendarDate, todayOnDevice } from '@/pantry/dates';

declare const __SET_TIME_ZONE__: (zone: string | undefined) => string | undefined;

describe('addDays', () => {
  test.each([
    ['2026-11-02', 7, '2026-11-09'],
    ['2026-01-31', 1, '2026-02-01'],
    ['2026-04-30', 1, '2026-05-01'],
    ['2026-02-28', 1, '2026-03-01'],
    ['2028-02-28', 1, '2028-02-29'],
    ['2028-02-29', 1, '2028-03-01'],
    ['2028-02-29', 365, '2029-02-28'],
    ['2026-12-31', 1, '2027-01-01'],
    ['2026-12-25', 10, '2027-01-04'],
    ['2026-03-01', -1, '2026-02-28'],
    ['2028-03-01', -1, '2028-02-29'],
    ['2027-01-01', -1, '2026-12-31'],
    ['2026-11-02', 0, '2026-11-02'],
    ['2026-11-02', 3650, '2036-10-30'],
    ['2000-02-28', 1, '2000-02-29'],
    ['2100-02-28', 1, '2100-03-01'],
  ])('%s plus %i days is %s', (date, days, expected) => {
    expect(addDays(date, days)).toBe(expected);
  });

  test('gives the same day in any time zone', () => {
    const previous = __SET_TIME_ZONE__('Pacific/Kiritimati');
    try {
      expect(addDays('2026-03-08', 1)).toBe('2026-03-09');
      __SET_TIME_ZONE__('America/Los_Angeles');
      expect(addDays('2026-03-08', 1)).toBe('2026-03-09');
      expect(addDays('2026-11-01', 1)).toBe('2026-11-02');
    } finally {
      __SET_TIME_ZONE__(previous);
    }
  });

  test('throws for something that is not a date', () => {
    expect(() => addDays('2026-02-30', 1)).toThrow(RangeError);
    expect(() => addDays('', 1)).toThrow(RangeError);
  });
});

describe('isValidCalendarDate', () => {
  test.each([
    ['2026-11-02', true],
    ['2028-02-29', true],
    ['2026-02-29', false],
    ['2026-13-01', false],
    ['2026-00-10', false],
    ['2026-04-31', false],
    ['2026-1-2', false],
    ['', false],
    ['not a date', false],
  ])('%s is %s', (value, expected) => {
    expect(isValidCalendarDate(value)).toBe(expected);
  });
});

describe('todayOnDevice', () => {
  test("is the device's local date, not the UTC date", () => {
    // 03:30 UTC on Nov 3 is still Nov 2 in Toronto, and already Nov 3 in Tokyo.
    const instant = new Date('2026-11-03T03:30:00Z');
    const previous = __SET_TIME_ZONE__('America/Toronto');
    try {
      expect(todayOnDevice(instant)).toBe('2026-11-02');
      __SET_TIME_ZONE__('Asia/Tokyo');
      expect(todayOnDevice(instant)).toBe('2026-11-03');
      // 23:30 UTC on Dec 31 is already Jan 1 in Tokyo.
      expect(todayOnDevice(new Date('2026-12-31T23:30:00Z'))).toBe('2027-01-01');
    } finally {
      __SET_TIME_ZONE__(previous);
    }
  });

  test('defaults to now', () => {
    const now = new Date();
    const expected = `${now.getFullYear()}-${String(now.getMonth() + 1).padStart(2, '0')}-${String(now.getDate()).padStart(2, '0')}`;
    expect(todayOnDevice()).toBe(expected);
  });
});

describe('date picker conversions', () => {
  test("a picker date is read as the device's calendar day", () => {
    const previous = __SET_TIME_ZONE__('America/Los_Angeles');
    try {
      expect(fromPickerDate(toPickerDate('2026-11-02'))).toBe('2026-11-02');
      expect(fromPickerDate(new Date(2026, 0, 31, 23, 59))).toBe('2026-01-31');
    } finally {
      __SET_TIME_ZONE__(previous);
    }
  });
});

describe('newId', () => {
  test('makes UUIDv7s (version nibble 7, RFC variant) that are all different', () => {
    const ids = Array.from({ length: 50 }, () => newId());
    for (const id of ids) {
      expect(id).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    }
    expect(new Set(ids).size).toBe(ids.length);
  });

  test('starts with the current time in milliseconds', () => {
    const before = Date.now();
    const id = newId();
    const millis = parseInt(id.replace(/-/g, '').slice(0, 12), 16);
    expect(millis).toBeGreaterThanOrEqual(before);
    expect(millis).toBeLessThanOrEqual(Date.now());
  });
});

describe('item names', () => {
  test('cleaning trims and collapses inner whitespace, like the API', () => {
    expect(cleanItemName('  oat   milk \t ')).toBe('oat milk');
  });

  test('names are the same ignoring case and extra spaces', () => {
    expect(sameItemName('Oat Milk', '  oat   milk ')).toBe(true);
    expect(sameItemName('Oat milk', 'Oat milks')).toBe(false);
  });
});

describe('withRetries', () => {
  const network = () => new ApiError('network', 'No response from the server');
  const http = (status: number) => new ApiError('http', `HTTP ${status}`, { status });

  test('retries network errors and 5xx up to 2 times', async () => {
    const send = jest.fn().mockRejectedValueOnce(network()).mockRejectedValueOnce(http(503)).mockResolvedValue('ok');
    await expect(withRetries(send, () => 0)).resolves.toBe('ok');
    expect(send).toHaveBeenCalledTimes(3);
  });

  test('gives up after the third failure', async () => {
    const send = jest.fn().mockRejectedValue(http(500));
    await expect(withRetries(send, () => 0)).rejects.toMatchObject({ status: 500 });
    expect(send).toHaveBeenCalledTimes(3);
  });

  test('never retries a 4xx', async () => {
    const send = jest.fn().mockRejectedValue(http(409));
    await expect(withRetries(send, () => 0)).rejects.toMatchObject({ status: 409 });
    expect(send).toHaveBeenCalledTimes(1);
  });
});

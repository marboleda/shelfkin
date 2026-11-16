import {
  daysBetween,
  expiryLabel,
  formatCalendarDate,
  groupAccessibilityLabel,
  lastChangeLabel,
  levelLabel,
  relativeTime,
  summarizeGroup,
  type LastChange,
} from '@/pantry/format';

const TODAY = '2026-11-02';

// Changes the test process's time zone (see jest/environment.js) and returns the previous one.
declare const __SET_TIME_ZONE__: (zone: string | undefined) => string | undefined;

describe('formatCalendarDate', () => {
  test('a date in the current year has no year', () => {
    expect(formatCalendarDate('2026-10-20', TODAY)).toBe('Oct 20');
  });

  test('a date in another year has its year', () => {
    expect(formatCalendarDate('2025-10-20', TODAY)).toBe('Oct 20, 2025');
    expect(formatCalendarDate('2027-01-03', TODAY)).toBe('Jan 3, 2027');
  });

  test('the current year comes from the API\'s today, not the device clock', () => {
    expect(formatCalendarDate('2025-12-31', '2025-12-31')).toBe('Dec 31');
  });

  describe.each(['America/Toronto', 'Asia/Tokyo', 'Pacific/Honolulu', 'Pacific/Kiritimati'])('with TZ=%s', (zone) => {
    let originalTz: string | undefined;
    beforeAll(() => {
      originalTz = __SET_TIME_ZONE__(zone);
    });
    afterAll(() => {
      __SET_TIME_ZONE__(originalTz);
    });

    test('the time zone is in effect', () => {
      // Guards the tests below: they would prove nothing if TZ were ignored.
      expect(Intl.DateTimeFormat().resolvedOptions().timeZone).toBe(zone);
    });

    test('2026-10-20 shows Oct 20', () => {
      expect(formatCalendarDate('2026-10-20', TODAY)).toBe('Oct 20');
      expect(formatCalendarDate('2026-01-01', TODAY)).toBe('Jan 1');
      expect(formatCalendarDate('2026-12-31', TODAY)).toBe('Dec 31');
      expect(expiryLabel({ level: 'full', expiresOn: '2026-10-25', expiryStatus: 'expired' }, TODAY).text).toBe(
        'Expired Oct 25',
      );
    });
  });

  test('Toronto and Tokyo give the same calendar date', () => {
    const originalTz = __SET_TIME_ZONE__('America/Toronto');
    try {
      const toronto = formatCalendarDate('2026-10-20', TODAY);
      const torontoOffset = new Date(2026, 9, 20).getTimezoneOffset();
      __SET_TIME_ZONE__('Asia/Tokyo');
      const tokyo = formatCalendarDate('2026-10-20', TODAY);
      const tokyoOffset = new Date(2026, 9, 20).getTimezoneOffset();

      expect(torontoOffset).not.toBe(tokyoOffset);
      expect(toronto).toBe('Oct 20');
      expect(tokyo).toBe('Oct 20');
    } finally {
      __SET_TIME_ZONE__(originalTz);
    }
  });
});

describe('daysBetween', () => {
  test('counts calendar days, across a DST change and a year end', () => {
    expect(daysBetween('2026-11-02', '2026-11-02')).toBe(0);
    expect(daysBetween('2026-10-31', '2026-11-03')).toBe(3);
    expect(daysBetween('2026-12-30', '2027-01-02')).toBe(3);
    expect(daysBetween('2026-11-02', '2026-10-25')).toBe(-8);
  });
});

describe('levelLabel', () => {
  test.each([
    ['full', 'Full'],
    ['half', 'Half'],
    ['low', 'Low'],
    ['out', 'Out'],
  ] as const)('%s is %s', (level, label) => {
    expect(levelLabel(level)).toBe(label);
  });
});

describe('expiryLabel', () => {
  test.each([
    ['2026-11-02', 'Expires today'],
    ['2026-11-03', 'Expires tomorrow'],
    ['2026-11-04', 'Expires in 2 days'],
    ['2026-11-05', 'Expires in 3 days'],
  ])('soon, %s: %s, highlighted', (expiresOn, text) => {
    expect(expiryLabel({ level: 'half', expiresOn, expiryStatus: 'soon' }, TODAY)).toEqual({ text, tone: 'soon' });
  });

  test('expired: "Expired Oct 25", in the danger tone', () => {
    expect(expiryLabel({ level: 'low', expiresOn: '2026-10-25', expiryStatus: 'expired' }, TODAY)).toEqual({
      text: 'Expired Oct 25',
      tone: 'expired',
    });
  });

  test('a later expiry is plain', () => {
    expect(expiryLabel({ level: 'full', expiresOn: '2026-11-30', expiryStatus: null }, TODAY)).toEqual({
      text: 'Expires Nov 30',
      tone: 'plain',
    });
  });

  test('no expiry date', () => {
    expect(expiryLabel({ level: 'full', expiresOn: null, expiryStatus: null }, TODAY)).toEqual({
      text: 'No expiry',
      tone: 'plain',
    });
  });

  test('an out entry inside the window is muted, never highlighted (the API gives it no status)', () => {
    expect(expiryLabel({ level: 'out', expiresOn: '2026-11-03', expiryStatus: null }, TODAY)).toEqual({
      text: 'Expires Nov 3',
      tone: 'muted',
    });
    // Even if a status came through, out is never highlighted.
    expect(expiryLabel({ level: 'out', expiresOn: '2026-10-25', expiryStatus: 'expired' }, TODAY).tone).toBe('muted');
    expect(expiryLabel({ level: 'out', expiresOn: null, expiryStatus: null }, TODAY)).toEqual({
      text: 'No expiry',
      tone: 'muted',
    });
  });

  test('the status comes from the API, not from the dates', () => {
    // A date in the window that the API did not mark soon is not highlighted.
    expect(expiryLabel({ level: 'full', expiresOn: '2026-11-03', expiryStatus: null }, TODAY).tone).toBe('plain');
  });
});

describe('relativeTime', () => {
  const now = new Date('2026-11-02T18:00:00Z');
  const ago = (ms: number) => new Date(now.getTime() - ms);
  const SECOND = 1000;
  const MINUTE = 60 * SECOND;
  const HOUR = 60 * MINUTE;
  const DAY = 24 * HOUR;

  test.each([
    ['0 s', 0, 'just now'],
    ['59 s', 59 * SECOND, 'just now'],
    ['1 min', MINUTE, '1m ago'],
    ['5 min', 5 * MINUTE, '5m ago'],
    ['59 min', 59 * MINUTE, '59m ago'],
    ['1 h', HOUR, '1h ago'],
    ['23 h', 23 * HOUR, '23h ago'],
    ['24 h', DAY, 'yesterday'],
    ['47 h', 47 * HOUR, 'yesterday'],
    ['2 d', 2 * DAY, '2d ago'],
    ['6 d', 6 * DAY + 23 * HOUR, '6d ago'],
  ])('%s ago: %s', (_name, ms, label) => {
    expect(relativeTime(ago(ms), now)).toBe(label);
  });

  test('from 7 days, the date', () => {
    expect(relativeTime(new Date('2026-10-20T12:00:00'), new Date('2026-10-27T12:00:00'))).toBe('Oct 20');
  });

  test('a date in another year has its year', () => {
    expect(relativeTime(new Date('2025-12-20T12:00:00'), new Date('2026-01-05T12:00:00'))).toBe('Dec 20, 2025');
  });

  test('a time slightly in the future is just now', () => {
    expect(relativeTime(new Date(now.getTime() + 5 * SECOND), now)).toBe('just now');
  });

  test('accepts RFC 3339 strings', () => {
    expect(relativeTime('2026-11-02T16:00:00Z', now)).toBe('2h ago');
  });
});

describe('lastChangeLabel', () => {
  const now = new Date('2026-11-02T18:00:00Z');
  const SAM = '0190f3a1-7c2e-7b3d-9a4f-00000000a001';
  const ME = '0190f3a1-7c2e-7b3d-9a4f-00000000b002';
  const locationName = (id: number) => ({ 1: 'Fridge', 2: 'Freezer' })[id];
  const context = { currentUserId: ME, locationName, now };

  function change(type: LastChange['type'], changes: LastChange['changes'] = {}, overrides: Partial<LastChange> = {}): LastChange {
    return { type, changes, actorId: SAM, actorName: 'Sam', occurredAt: '2026-11-02T16:00:00Z', ...overrides };
  }

  test.each<[string, LastChange, string]>([
    ['entry_created', change('entry_created'), 'Added by Sam, 2h ago'],
    ['level_changed', change('level_changed', { level: { from: 'half', to: 'low' } }), 'Set to Low by Sam, 2h ago'],
    [
      'location_changed',
      change('location_changed', { location_id: { from: 1, to: 2 } }),
      'Moved to Freezer by Sam, 2h ago',
    ],
    [
      'expiry_changed to a date',
      change('expiry_changed', { expires_on: { from: '2026-11-05', to: '2026-11-09' } }),
      'Expiry changed by Sam, 2h ago',
    ],
    [
      'expiry_changed from no date',
      change('expiry_changed', { expires_on: { from: null, to: '2026-11-09' } }),
      'Expiry changed by Sam, 2h ago',
    ],
    [
      'expiry_changed to null',
      change('expiry_changed', { expires_on: { from: '2026-11-05', to: null } }),
      'Expiry removed by Sam, 2h ago',
    ],
    [
      'purchase_date_changed',
      change('purchase_date_changed', { purchased_on: { from: '2026-10-20', to: '2026-10-21' } }),
      'Purchase date changed by Sam, 2h ago',
    ],
  ])('%s', (_name, value, label) => {
    expect(lastChangeLabel(value, context)).toBe(label);
  });

  test('"by you" when the actor is the signed-in user', () => {
    expect(
      lastChangeLabel(change('level_changed', { level: { from: 'full', to: 'half' } }, { actorId: ME, actorName: 'Alex' }), context),
    ).toBe('Set to Half by you, 2h ago');
  });

  test('no name when the actor\'s account was deleted', () => {
    expect(
      lastChangeLabel(change('level_changed', { level: { from: 'half', to: 'low' } }, { actorId: null, actorName: null }), context),
    ).toBe('Set to Low, 2h ago');
  });

  test('the name when the signed-in user is not known yet', () => {
    expect(lastChangeLabel(change('entry_created'), { now, locationName })).toBe('Added by Sam, 2h ago');
  });

  test('a location whose name is not loaded yet', () => {
    expect(lastChangeLabel(change('location_changed', { location_id: { from: 1, to: 9 } }), context)).toBe(
      'Moved by Sam, 2h ago',
    );
  });

  test('null when there is no recorded change', () => {
    expect(lastChangeLabel(null, context)).toBeNull();
  });
});

describe('summarizeGroup', () => {
  test('lists the levels in entry order', () => {
    const summary = summarizeGroup(
      [
        { level: 'full', expiresOn: null, expiryStatus: null },
        { level: 'low', expiresOn: null, expiryStatus: null },
      ],
      TODAY,
    );
    expect(summary).toEqual({ levels: 'Full, Low', expiry: null, highlighted: false, allOut: false });
  });

  test('shows the most urgent expiry among entries that are not out', () => {
    const summary = summarizeGroup(
      [
        { level: 'full', expiresOn: '2026-11-20', expiryStatus: null },
        { level: 'out', expiresOn: '2026-10-28', expiryStatus: null },
        { level: 'low', expiresOn: '2026-11-03', expiryStatus: 'soon' },
      ],
      TODAY,
    );
    expect(summary.expiry).toEqual({ text: 'Expires tomorrow', tone: 'soon' });
    expect(summary.highlighted).toBe(true);
  });

  test('an expired entry is more urgent than a soon one', () => {
    const summary = summarizeGroup(
      [
        { level: 'full', expiresOn: '2026-11-03', expiryStatus: 'soon' },
        { level: 'half', expiresOn: '2026-10-25', expiryStatus: 'expired' },
      ],
      TODAY,
    );
    expect(summary.expiry).toEqual({ text: 'Expired Oct 25', tone: 'expired' });
  });

  test('a later expiry is shown but not highlighted', () => {
    const summary = summarizeGroup(
      [
        { level: 'full', expiresOn: '2026-12-12', expiryStatus: null },
        { level: 'full', expiresOn: null, expiryStatus: null },
      ],
      TODAY,
    );
    expect(summary.expiry).toEqual({ text: 'Expires Dec 12', tone: 'plain' });
    expect(summary.highlighted).toBe(false);
  });

  test('all out: muted, never highlighted, no expiry', () => {
    const summary = summarizeGroup(
      [
        { level: 'out', expiresOn: '2026-11-03', expiryStatus: null },
        { level: 'out', expiresOn: null, expiryStatus: null },
      ],
      TODAY,
    );
    expect(summary).toEqual({ levels: 'Out, Out', expiry: null, highlighted: false, allOut: true });
  });
});

describe('groupAccessibilityLabel', () => {
  test('"Milk, 2 entries, one expires tomorrow"', () => {
    expect(
      groupAccessibilityLabel(
        'Milk',
        [
          { level: 'full', expiresOn: '2026-11-10', expiryStatus: null },
          { level: 'low', expiresOn: '2026-11-03', expiryStatus: 'soon' },
        ],
        TODAY,
      ),
    ).toBe('Milk, 2 entries, Full, Low, one expires tomorrow');
  });

  test('an expired entry', () => {
    expect(
      groupAccessibilityLabel(
        'Milk',
        [
          { level: 'full', expiresOn: null, expiryStatus: null },
          { level: 'low', expiresOn: '2026-10-25', expiryStatus: 'expired' },
        ],
        TODAY,
      ),
    ).toBe('Milk, 2 entries, Full, Low, one expired Oct 25');
  });

  test('several urgent entries', () => {
    expect(
      groupAccessibilityLabel(
        'Milk',
        [
          { level: 'full', expiresOn: '2026-11-04', expiryStatus: 'soon' },
          { level: 'low', expiresOn: '2026-11-02', expiryStatus: 'soon' },
          { level: 'out', expiresOn: '2026-11-02', expiryStatus: null },
        ],
        TODAY,
      ),
    ).toBe('Milk, 3 entries, Full, Low, Out, 2 expiring soon or expired, the first expires today');
  });

  test('all out', () => {
    expect(
      groupAccessibilityLabel(
        'Rice',
        [
          { level: 'out', expiresOn: null, expiryStatus: null },
          { level: 'out', expiresOn: null, expiryStatus: null },
        ],
        TODAY,
      ),
    ).toBe('Rice, 2 entries, all out');
  });
});

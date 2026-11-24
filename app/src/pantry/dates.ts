// Calendar-date maths on `YYYY-MM-DD` strings: today's date on the device and
// adding days. A calendar date names a day, not an instant, so these never go
// through UTC timestamps (see the note at the top of format.ts): "today" is read
// from the device's local clock fields, and adding days counts whole days with
// the civil-calendar algorithm, so the device's time zone can never shift a day.

import { parseCalendarDate } from './format';

function pad(value: number, width: number): string {
  return String(value).padStart(width, '0');
}

function formatParts(year: number, month: number, day: number): string {
  return `${pad(year, 4)}-${pad(month, 2)}-${pad(day, 2)}`;
}

/** Today's date in the device's time zone, as `YYYY-MM-DD`. */
export function todayOnDevice(now: Date = new Date()): string {
  return formatParts(now.getFullYear(), now.getMonth() + 1, now.getDate());
}

function isLeapYear(year: number): boolean {
  return (year % 4 === 0 && year % 100 !== 0) || year % 400 === 0;
}

function daysInMonth(year: number, month: number): number {
  if (month === 2) return isLeapYear(year) ? 29 : 28;
  return [4, 6, 9, 11].includes(month) ? 30 : 31;
}

/** Whether `value` is a real `YYYY-MM-DD` date (not "2026-02-30" or "2026-13-01"). */
export function isValidCalendarDate(value: string): boolean {
  const date = parseCalendarDate(value);
  if (!date) return false;
  return date.month >= 1 && date.month <= 12 && date.day >= 1 && date.day <= daysInMonth(date.year, date.month);
}

// Days since 1970-01-01 for a proleptic Gregorian date, and back
// (Howard Hinnant's `days_from_civil` / `civil_from_days`).
function daysFromCivil(year: number, month: number, day: number): number {
  const y = month <= 2 ? year - 1 : year;
  const era = Math.floor(y / 400);
  const yoe = y - era * 400;
  const doy = Math.floor((153 * (month + (month > 2 ? -3 : 9)) + 2) / 5) + day - 1;
  const doe = yoe * 365 + Math.floor(yoe / 4) - Math.floor(yoe / 100) + doy;
  return era * 146097 + doe - 719468;
}

function civilFromDays(days: number): { year: number; month: number; day: number } {
  const z = days + 719468;
  const era = Math.floor(z / 146097);
  const doe = z - era * 146097;
  const yoe = Math.floor((doe - Math.floor(doe / 1460) + Math.floor(doe / 36524) - Math.floor(doe / 146096)) / 365);
  const doy = doe - (365 * yoe + Math.floor(yoe / 4) - Math.floor(yoe / 100));
  const mp = Math.floor((5 * doy + 2) / 153);
  const day = doy - Math.floor((153 * mp + 2) / 5) + 1;
  const month = mp < 10 ? mp + 3 : mp - 9;
  const year = yoe + era * 400 + (month <= 2 ? 1 : 0);
  return { year, month, day };
}

/**
 * The date `days` days after `date` (before it when negative), both `YYYY-MM-DD`.
 * Throws for a value that is not a valid date.
 */
export function addDays(date: string, days: number): string {
  if (!isValidCalendarDate(date)) throw new RangeError(`Not a calendar date: ${date}`);
  const parts = parseCalendarDate(date)!;
  const result = civilFromDays(daysFromCivil(parts.year, parts.month, parts.day) + Math.trunc(days));
  return formatParts(result.year, result.month, result.day);
}

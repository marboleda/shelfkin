/** The props of `DateField` (date-field.tsx on iOS and Android, date-field.web.tsx on web). */
export type DateFieldProps = {
  label: string;
  /** A `YYYY-MM-DD` date, or '' for none. */
  value: string;
  /** Called with a `YYYY-MM-DD` date, or '' when the date is cleared (or emptied on web). */
  onChange: (value: string) => void;
  /** Shown under the field, and announced, when set. */
  error?: string | null;
  /** Shown under the field when there is no error. */
  hint?: string | null;
  /** The accessible name of a clear control, which is only shown when this is set and there is a date. */
  clearLabel?: string;
  disabled?: boolean;
  testID?: string;
};

/** A `YYYY-MM-DD` date as a local `Date` at midnight, for a native picker; today when empty or invalid. */
export function toPickerDate(value: string, fallback: Date = new Date()): Date {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(value);
  if (!match) return fallback;
  return new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]));
}

/** The calendar day a native picker chose, in the device's time zone, as `YYYY-MM-DD`. */
export function fromPickerDate(date: Date): string {
  const pad = (n: number, width = 2) => String(n).padStart(width, '0');
  return `${pad(date.getFullYear(), 4)}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

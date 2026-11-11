import { getCalendars } from 'expo-localization';

/**
 * The device's IANA time zone name, such as `America/Toronto`, or null when
 * the device does not report one. Never returns abbreviations or offsets.
 */
export function deviceTimeZone(): string | null {
  let zone: string | null | undefined;
  try {
    zone = getCalendars()[0]?.timeZone;
  } catch {
    zone = undefined;
  }
  if (!zone) {
    try {
      zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
    } catch {
      zone = undefined;
    }
  }
  // IANA names are `Area/Location` (or `UTC`); anything else is not sent.
  return zone && (zone === 'UTC' || zone.includes('/')) ? zone : null;
}

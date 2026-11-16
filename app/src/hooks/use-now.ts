import { useEffect, useState } from 'react';

/** How often `useNow` ticks by default: often enough that "5m ago" labels are never a minute behind. */
export const NOW_TICK_MS = 30_000;

/** The current time, updated every `intervalMs` while the component is mounted (for relative-time labels). */
export function useNow(intervalMs = NOW_TICK_MS): Date {
  const [now, setNow] = useState(() => new Date());
  useEffect(() => {
    const timer = setInterval(() => setNow(new Date()), intervalMs);
    return () => clearInterval(timer);
  }, [intervalMs]);
  return now;
}

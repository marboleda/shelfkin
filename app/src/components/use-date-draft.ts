// What an editable saved date shows while it is being changed, and when the
// change is saved. A native picker returns one finished date, which is saved at
// once. On web the date is typed into `<input type="date">`, which reports a
// "valid" date after every segment and every digit of the year (0002, 0020,
// 0202, 2026), so a typed date is only saved when the person has finished:
// when they leave the field, press Enter, leave the screen or hide the page.
// (Leaving a screen that stays mounted is up to the screen: call `commit` when
// it loses focus.)

import { useCallback, useEffect, useRef, useState } from 'react';
import { Platform } from 'react-native';

import { isPlausibleCalendarDate, isValidCalendarDate } from '@/pantry/dates';

/**
 * When a changed date is saved: `'change'` saves each date it is given (a
 * native picker); `'commit'` saves a typed date on blur, Enter, unmount or
 * when the page is hidden, and treats a year outside 2000-2099 as unfinished.
 */
export type DateSaveMode = 'change' | 'commit';

/** How this platform's `DateField` reports dates (see `DateSaveMode`). */
export const DATE_SAVE_MODE: DateSaveMode = Platform.OS === 'web' ? 'commit' : 'change';

type Draft = {
  /** What the field holds. */
  text: string;
  /** The saved date when the draft was typed; the draft is dropped once that changes. */
  saved: string;
  /** Whether `text` has been handed to `onSave` already. */
  committed: boolean;
};

/** How a save was triggered. `keepalive`: the page is being hidden and may unload right after. */
export type DateSaveOptions = { keepalive?: boolean };

export type DateDraftStatus =
  /** The field holds something that is not a whole date, or was emptied. */
  | 'incomplete'
  /** The field holds a whole date that is not saved yet (commit mode only). */
  | 'unsaved'
  | null;

/**
 * Keeps what is typed into a date field until it is saved. A draft is never
 * replaced by the saved date while the saved date stays the same, and is
 * dropped as soon as the saved date changes (a save, a rollback, someone
 * else's change after a refetch).
 */
export function useDateDraft(
  value: string,
  onSave: (value: string, options?: DateSaveOptions) => void,
  mode: DateSaveMode,
) {
  const [draft, setDraftState] = useState<Draft | null>(null);
  const draftRef = useRef<Draft | null>(null);
  const latest = useRef({ value, onSave });
  useEffect(() => {
    latest.current = { value, onSave };
  });

  const isComplete = mode === 'commit' ? isPlausibleCalendarDate : isValidCalendarDate;

  const setDraft = (next: Draft | null) => {
    draftRef.current = next;
    setDraftState(next);
  };

  // Forget a draft for a saved date that has since changed, so a rollback to
  // that date later doesn't bring the draft back.
  const current = draft && draft.saved === value ? draft : null;
  if (draft && !current) setDraftState(null);
  useEffect(() => {
    if (draftRef.current && draftRef.current.saved !== value) draftRef.current = null;
  }, [value]);

  const commitNow = (options?: DateSaveOptions) => {
    const pending = draftRef.current;
    const saved = latest.current.value;
    if (!pending || pending.committed || pending.saved !== saved) return;
    if (pending.text === saved || !isComplete(pending.text)) return;
    // Keep showing the date until the saved date changes to it (the save is optimistic, but not synchronous).
    setDraft({ ...pending, committed: true });
    if (options) latest.current.onSave(pending.text, options);
    else latest.current.onSave(pending.text);
  };
  const commitRef = useRef(commitNow);
  useEffect(() => {
    commitRef.current = commitNow;
  });
  /**
   * Saves the typed date if it is a whole date that differs from the saved one
   * (commit mode). The same function on every render, so it can be used in effects.
   */
  const commit = useCallback(() => commitRef.current(), []);

  // Save a date that is still waiting when the screen goes away.
  useEffect(() => () => commitRef.current(), []);

  // ...or when the page is hidden: switching tabs, closing it or navigating away. The page may
  // unload right after, so the save is asked to leave at once (`keepalive`).
  useEffect(() => {
    if (mode !== 'commit' || typeof document === 'undefined') return;
    if (typeof window === 'undefined' || typeof window.addEventListener !== 'function') return;
    const doc = document;
    const win = window;
    const onVisibilityChange = () => {
      if (doc.visibilityState === 'hidden') commitRef.current({ keepalive: true });
    };
    const onPageHide = () => commitRef.current({ keepalive: true });
    doc.addEventListener('visibilitychange', onVisibilityChange);
    win.addEventListener('pagehide', onPageHide);
    return () => {
      doc.removeEventListener('visibilitychange', onVisibilityChange);
      win.removeEventListener('pagehide', onPageHide);
    };
  }, [mode]);

  const onChange = (text: string) => {
    if (!isComplete(text)) {
      setDraft({ text, saved: value, committed: false });
      return;
    }
    if (mode === 'change') {
      setDraft(null);
      onSave(text);
      return;
    }
    setDraft(text === value ? null : { text, saved: value, committed: false });
  };

  const shown = current ? current.text : value;
  let status: DateDraftStatus = null;
  if ((shown !== '' && !isComplete(shown)) || (shown === '' && value !== '')) status = 'incomplete';
  else if (mode === 'commit' && current && !current.committed && current.text !== value) status = 'unsaved';

  return { shown, status, onChange, commit };
}

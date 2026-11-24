// The signed-in user's Pantry view mode and filters: read from the device once
// per user, and saved as soon as they change.

import { useCallback, useEffect, useState } from 'react';

import { useAuth } from '@/auth/session';

import { loadPantryView, savePantryView, type PantryViewChoices } from './view-storage';

export type PantryView = {
  /** False until the saved choices have been read, so the list never starts in the default view and then jumps. */
  loaded: boolean;
  choices: PantryViewChoices | null;
  setChoices: (choices: PantryViewChoices) => void;
};

export function usePantryView(): PantryView {
  const auth = useAuth();
  const uid = auth.status === 'signedIn' ? auth.user.uid : null;
  const [state, setState] = useState<{ uid: string; choices: PantryViewChoices } | null>(null);

  useEffect(() => {
    if (!uid) return;
    let cancelled = false;
    void loadPantryView(uid).then((choices) => {
      if (cancelled) return;
      // A change made while loading wins over what was saved.
      setState((previous) => (previous?.uid === uid ? previous : { uid, choices }));
    });
    return () => {
      cancelled = true;
    };
  }, [uid]);

  const setChoices = useCallback(
    (choices: PantryViewChoices) => {
      if (!uid) return;
      setState({ uid, choices });
      void savePantryView(uid, choices);
    },
    [uid],
  );

  const loaded = uid !== null && state?.uid === uid;
  return { loaded, choices: loaded ? state.choices : null, setChoices };
}

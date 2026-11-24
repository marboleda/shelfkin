import { useFocusEffect } from 'expo-router';
import { useCallback, useState } from 'react';

import { AddItemForm } from '@/components/add-item-form';
import { BackToPantry } from '@/components/back-to-pantry';
import { Screen } from '@/components/screen';
import { useCurrentHousehold } from '@/household/current-household';

/**
 * Add item: search the catalog, pick an item (or create a new one), check its
 * details and add it, then go straight on to the next item.
 *
 * `/add` is a tab screen and stays mounted, so the form is only rendered while
 * the screen is focused: every visit (and every save) mounts a fresh form, and
 * leaving throws the form away without asking.
 */
export default function AddItemScreen() {
  const household = useCurrentHousehold();
  // Counts visits and saves; null while the screen is not focused.
  const [session, setSession] = useState<number | null>(null);
  const [confirmation, setConfirmation] = useState<string | null>(null);

  useFocusEffect(
    useCallback(() => {
      setSession((current) => (current ?? 0) + 1);
      return () => {
        setSession(null);
        setConfirmation(null);
      };
    }, []),
  );

  const onSaved = useCallback((message: string) => {
    setConfirmation(message);
    setSession((current) => (current ?? 0) + 1);
  }, []);

  return (
    <Screen title="Add item" inShell accessory={<BackToPantry />} scroll={false}>
      {session !== null ? (
        <AddItemForm key={`${household.id}:${session}`} onSaved={onSaved} confirmation={confirmation} />
      ) : null}
    </Screen>
  );
}

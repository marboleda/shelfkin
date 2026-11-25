import { useLocalSearchParams } from 'expo-router';

import { ItemSettings } from '@/components/item-settings';

/**
 * `/items/<catalogItemId>`: the current household's defaults for one catalog
 * item (see ItemSettings). `?from=<entryId>` makes the back link return to that entry.
 */
export default function ItemSettingsScreen() {
  const { id, from } = useLocalSearchParams<{ id: string; from?: string }>();
  return <ItemSettings itemId={String(id ?? '')} fromEntryId={typeof from === 'string' && from !== '' ? from : null} />;
}

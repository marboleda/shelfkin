import { useLocalSearchParams } from 'expo-router';

import { EntryDetail } from '@/components/entry-detail';

/** `/entries/<id>`: one entry of the current household's pantry (see EntryDetail). */
export default function EntryDetailScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  return <EntryDetail entryId={String(id ?? '')} />;
}

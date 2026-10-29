import { useLocalSearchParams } from 'expo-router';

import { BackToPantry } from '@/components/back-to-pantry';
import { Screen } from '@/components/screen';
import { ThemedText } from '@/components/themed-text';

export default function EntryDetailScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();

  return (
    <Screen title="Entry detail" inShell accessory={<BackToPantry />}>
      <ThemedText testID="entry-id">{id}</ThemedText>
      <ThemedText variant="muted">Details for this pantry entry will appear here.</ThemedText>
    </Screen>
  );
}

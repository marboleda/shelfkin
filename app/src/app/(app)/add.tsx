import { BackToPantry } from '@/components/back-to-pantry';
import { Screen } from '@/components/screen';
import { ThemedText } from '@/components/themed-text';

export default function AddItemScreen() {
  return (
    <Screen title="Add item" inShell accessory={<BackToPantry />}>
      <ThemedText variant="muted">The add item form will appear here.</ThemedText>
    </Screen>
  );
}

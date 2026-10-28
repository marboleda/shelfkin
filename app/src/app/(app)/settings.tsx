import { Screen } from '@/components/screen';
import { ThemedText } from '@/components/themed-text';

export default function SettingsScreen() {
  return (
    <Screen title="Settings" inShell>
      <ThemedText variant="muted">Household and notification settings will appear here.</ThemedText>
    </Screen>
  );
}

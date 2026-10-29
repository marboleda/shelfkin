import { Screen } from '@/components/screen';
import { ServerStatus } from '@/components/server-status';
import { ThemedText } from '@/components/themed-text';

export default function SettingsScreen() {
  return (
    <Screen title="Settings" inShell>
      <ServerStatus />
      <ThemedText variant="muted">Household and notification settings will appear here.</ThemedText>
    </Screen>
  );
}

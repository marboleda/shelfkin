import { Screen } from '@/components/screen';
import { ThemedText } from '@/components/themed-text';

export default function OnboardingScreen() {
  return (
    <Screen title="Onboarding">
      <ThemedText variant="muted">Create or join a household here.</ThemedText>
    </Screen>
  );
}

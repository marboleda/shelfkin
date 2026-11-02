import { Screen } from './screen';
import { ThemedText } from './themed-text';

/** Shown instead of the app when the build is missing required configuration. */
export function ConfigError({ message }: { message: string }) {
  return (
    <Screen title="App not configured">
      <ThemedText testID="config-error">{message}</ThemedText>
    </Screen>
  );
}

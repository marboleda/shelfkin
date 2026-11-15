import { ActivityIndicator, Switch, Text, View } from 'react-native';

import { openSystemSettings } from '@/push/device';
import { BLOCKED_MESSAGE, usePushSettings } from '@/push/use-push-settings';
import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

import { Button } from './button';
import { FormMessage } from './form-message';

const ACCOUNT_NOTE = 'A daily summary at about 9am when items are about to expire';
const WEB_NOTE = 'Reminders are sent to the Shelfkin app on your phone';

/**
 * The Notifications section on Settings: "Expiry reminders" for the account
 * (all the user's devices) and, on a phone while that is on, "On this device".
 */
export function NotificationsSection() {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const push = usePushSettings();

  const accountValue = push.pending?.which === 'account' ? push.pending.value : (push.accountOn ?? false);
  const deviceValue = push.pending?.which === 'device' ? push.pending.value : (push.deviceOn ?? false);
  const busy = push.pending !== null;
  const showDevice = push.showDeviceSwitch && accountValue && push.accountOn !== undefined;
  const blocked = push.permission === 'blocked';

  const deviceNote = !push.deviceSupported
    ? 'Needs a physical phone'
    : blocked
      ? 'Blocked in system settings'
      : null;

  const switchColors = {
    trackColor: { false: theme.colors.mutedText, true: theme.colors.primary },
    thumbColor: theme.colors.surface,
    ios_backgroundColor: theme.colors.mutedText,
  };

  return (
    <View testID="notifications-section" style={styles.section}>
      <Text role="heading" aria-level={2} style={styles.sectionTitle}>
        Notifications
      </Text>
      <View style={styles.card}>
        <View style={styles.row}>
          <View style={styles.rowText}>
            <Text nativeID="push-account-label" style={styles.rowLabel}>
              Expiry reminders
            </Text>
            <Text style={styles.rowNote}>{ACCOUNT_NOTE}</Text>
            {!push.showDeviceSwitch ? (
              <Text testID="push-web-note" style={styles.rowNote}>
                {WEB_NOTE}
              </Text>
            ) : null}
          </View>
          {push.pending?.which === 'account' ? (
            <ActivityIndicator size="small" color={theme.colors.mutedText} />
          ) : null}
          <Switch
            testID="push-account-switch"
            accessibilityLabel="Expiry reminders"
            accessibilityState={{ checked: accountValue, disabled: busy || push.accountOn === undefined, busy }}
            value={accountValue}
            disabled={busy || push.accountOn === undefined}
            onValueChange={push.setAccount}
            {...switchColors}
          />
        </View>
        {showDevice ? (
          <View style={[styles.row, styles.divider]}>
            <View style={styles.rowText}>
              <Text style={styles.rowLabel}>On this device</Text>
              {deviceNote ? (
                <Text testID="push-device-note" style={styles.rowNote}>
                  {deviceNote}
                </Text>
              ) : null}
            </View>
            {push.pending?.which === 'device' ? (
              <ActivityIndicator size="small" color={theme.colors.mutedText} />
            ) : null}
            <Switch
              testID="push-device-switch"
              accessibilityLabel="On this device"
              accessibilityState={{ checked: deviceValue, disabled: busy || !push.deviceSupported, busy }}
              value={deviceValue}
              disabled={busy || !push.deviceSupported || push.deviceOn === undefined}
              onValueChange={push.setDevice}
              {...switchColors}
            />
          </View>
        ) : null}
      </View>
      {push.problem?.kind === 'blocked' ? (
        <FormMessage testID="push-blocked" tone="error" message={BLOCKED_MESSAGE}>
          <Button label="Open Settings" variant="link" onPress={() => void openSystemSettings().catch(() => {})} />
        </FormMessage>
      ) : push.problem?.kind === 'error' ? (
        <FormMessage testID="push-error" tone="error" message={push.problem.message} />
      ) : null}
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    section: { gap: theme.spacing.sm },
    sectionTitle: { ...theme.typography.overline, color: theme.colors.mutedText, paddingHorizontal: theme.spacing.xs },
    card: {
      backgroundColor: theme.colors.surface,
      borderColor: theme.colors.border,
      borderWidth: 1,
      borderRadius: theme.radii.card,
      overflow: 'hidden' as const,
    },
    row: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.md,
      minHeight: Math.max(56, MIN_TOUCH_TARGET),
      paddingVertical: theme.spacing.md,
      paddingHorizontal: theme.spacing.md + 2,
    },
    divider: { borderTopWidth: 1, borderTopColor: theme.colors.border },
    rowText: { flex: 1, gap: 3 },
    rowLabel: { ...theme.typography.label, fontSize: 16, color: theme.colors.text },
    rowNote: { ...theme.typography.caption, color: theme.colors.mutedText },
  };
}

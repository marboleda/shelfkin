// The app's confirmation dialog, shared by every screen that asks before doing
// something (household settings, deleting an entry in #30, ...).
//
// Built on React Native's Modal rather than `Alert.alert`, which does nothing
// on web. It works the same on iOS, Android and web: Escape (web), the back
// button (Android) and the screen reader escape gesture (iOS) cancel, and so
// does tapping outside the dialog. While `busy`, it cannot be dismissed and
// both buttons are disabled; the confirm button shows progress.

import { useState, type ReactNode } from 'react';
import { Modal, ScrollView, Text, View } from 'react-native';

import { useThemedStyles, type Theme } from '@/theme';

import { Button } from './button';
import { FormMessage } from './form-message';
import { TextField } from './text-field';

export type ConfirmDialogProps = {
  visible: boolean;
  title: string;
  /** The question, e.g. "Remove Sam from Home? They'll lose access to its pantry." */
  message?: string;
  confirmLabel: string;
  cancelLabel?: string;
  /** Draws the confirm button in the danger colour, for actions that remove or delete something. */
  destructive?: boolean;
  /** The confirm request is running: show progress and block both buttons and dismissing. */
  busy?: boolean;
  /** Shown above the buttons, e.g. why the last attempt failed. */
  error?: string | null;
  /** Disables the confirm button, e.g. until a choice is made in `children`. */
  confirmDisabled?: boolean;
  /**
   * Asks the user to type `expected` before the confirm button is enabled, for
   * actions that cannot be undone. Compared after trimming, case-sensitive.
   */
  typeToConfirm?: { label: string; expected: string };
  /** Extra content between the message and the buttons, such as a list to choose from. */
  children?: ReactNode;
  onConfirm: () => void;
  onCancel: () => void;
  /** Called once the dialog has finished closing. */
  onDismiss?: () => void;
  testID?: string;
};

export function ConfirmDialog({
  visible,
  title,
  message,
  confirmLabel,
  cancelLabel = 'Cancel',
  destructive = false,
  busy = false,
  error,
  confirmDisabled = false,
  typeToConfirm,
  children,
  onConfirm,
  onCancel,
  onDismiss,
  testID = 'confirm-dialog',
}: ConfirmDialogProps) {
  const styles = useThemedStyles(createStyles);
  const [typed, setTyped] = useState('');

  // Start empty each time the dialog opens.
  const [wasVisible, setWasVisible] = useState(visible);
  if (visible !== wasVisible) {
    setWasVisible(visible);
    if (visible) setTyped('');
  }

  const typedMatches = !typeToConfirm || typed.trim() === typeToConfirm.expected.trim();
  const canConfirm = !busy && !confirmDisabled && typedMatches;

  function cancel() {
    if (!busy) onCancel();
  }

  function confirm() {
    if (canConfirm) onConfirm();
  }

  return (
    <Modal
      visible={visible}
      transparent
      animationType="fade"
      onRequestClose={cancel}
      onDismiss={onDismiss}
      statusBarTranslucent
      navigationBarTranslucent
      aria-label={title}
    >
      <View style={styles.layer}>
        {/* Not a Pressable: on web it would take focus inside the Modal's focus trap. */}
        <View
          testID={`${testID}-backdrop`}
          onStartShouldSetResponder={() => true}
          onResponderRelease={cancel}
          accessible={false}
          importantForAccessibility="no"
          style={styles.backdrop}
        />
        <View
          testID={testID}
          role="alertdialog"
          aria-modal
          aria-label={title}
          accessibilityViewIsModal
          onAccessibilityEscape={cancel}
          style={styles.dialog}
        >
          <ScrollView contentContainerStyle={styles.body} keyboardShouldPersistTaps="handled">
            <Text role="heading" aria-level={2} style={styles.title}>
              {title}
            </Text>
            {message ? <Text style={styles.message}>{message}</Text> : null}
            {children}
            {typeToConfirm ? (
              <TextField
                label={typeToConfirm.label}
                testID={`${testID}-input`}
                value={typed}
                onChangeText={setTyped}
                autoCapitalize="none"
                autoCorrect={false}
                autoComplete="off"
                editable={!busy}
                returnKeyType="done"
                onSubmitEditing={confirm}
              />
            ) : null}
            {error ? <FormMessage tone="error" message={error} testID={`${testID}-error`} /> : null}
            <View style={styles.actions}>
              <Button
                label={confirmLabel}
                variant={destructive ? 'danger' : 'primary'}
                busy={busy}
                disabled={!canConfirm}
                onPress={confirm}
                testID={`${testID}-confirm`}
              />
              <Button label={cancelLabel} variant="secondary" disabled={busy} onPress={cancel} testID={`${testID}-cancel`} />
            </View>
          </ScrollView>
        </View>
      </View>
    </Modal>
  );
}

function createStyles(theme: Theme) {
  return {
    layer: {
      flex: 1,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      padding: theme.spacing.xl,
    },
    backdrop: {
      position: 'absolute' as const,
      top: 0,
      right: 0,
      bottom: 0,
      left: 0,
      // The text colour, faded, keeps the scrim on-theme in light and dark mode.
      backgroundColor: theme.colors.text,
      opacity: 0.45,
    },
    dialog: {
      width: '100%' as const,
      maxWidth: 440,
      maxHeight: '90%' as const,
      backgroundColor: theme.colors.background,
      borderRadius: theme.radii.lg,
      overflow: 'hidden' as const,
    },
    body: { gap: theme.spacing.lg, padding: theme.spacing.xl },
    title: { ...theme.typography.title, color: theme.colors.text },
    message: { ...theme.typography.body, color: theme.colors.text },
    actions: { gap: theme.spacing.sm },
  };
}

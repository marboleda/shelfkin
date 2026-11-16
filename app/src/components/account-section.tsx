import { useRouter } from 'expo-router';
import { useState } from 'react';
import { ActivityIndicator, Pressable, Text, View } from 'react-native';

import { userMessage } from '@/api/errors';
import {
  DELETE_ACCOUNT_MESSAGE,
  deleteAccountErrorMessage,
  householdsToTransfer,
  transferOwnershipMessage,
  useDeleteAccountMutation,
  type HouseholdRef,
} from '@/auth/delete-account';
import { useMeQuery, useUpdateNameMutation } from '@/auth/profile';
import { signOut, useAuth } from '@/auth/session';
import { NAME_MAX_LENGTH, validateName } from '@/auth/validation';
import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

import { useHouseholds } from '@/household/current-household';

import { Button } from './button';
import { ConfirmDialog } from './confirm-dialog';
import { FormMessage } from './form-message';
import { NotificationsSection } from './notifications-section';
import { TextField } from './text-field';

/** What to type in the confirmation before the account can be deleted. */
const DELETE_CONFIRM_WORD = 'DELETE';

/**
 * The Account section on Settings: name (editable), email, Sign out and
 * Delete account (#46).
 * The Notifications section (expiry reminders) comes right before it.
 */
export function AccountSection() {
  const styles = useThemedStyles(createStyles);
  const theme = useTheme();
  const auth = useAuth();
  const me = useMeQuery();
  const updateName = useUpdateNameMutation();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState('');
  const [nameError, setNameError] = useState<string | null>(null);
  const [signingOut, setSigningOut] = useState(false);
  const [signOutError, setSignOutError] = useState<string | null>(null);
  const deleteAccount = useDeleteAccountMutation();
  const [deleteDialogOpen, setDeleteDialogOpen] = useState(false);
  const [mustTransfer, setMustTransfer] = useState<HouseholdRef[] | null>(null);
  const households = useHouseholds();
  const router = useRouter();

  const email = me.data?.email ?? (auth.status === 'signedIn' ? auth.user.email : null);

  function startEditing() {
    setDraft(me.data?.displayName ?? '');
    setNameError(null);
    updateName.reset();
    setEditing(true);
  }

  function saveName() {
    if (updateName.isPending) return;
    const error = validateName(draft);
    setNameError(error);
    if (error) return;
    updateName.mutate(draft, { onSuccess: () => setEditing(false) });
  }

  async function handleSignOut() {
    if (signingOut) return;
    setSigningOut(true);
    setSignOutError(null);
    try {
      await signOut();
      // The layouts now send the user to /sign-in.
    } catch {
      setSignOutError("Couldn't sign out. Please try again.");
      setSigningOut(false);
    }
  }

  function openDeleteDialog() {
    deleteAccount.reset();
    setMustTransfer(null);
    setDeleteDialogOpen(true);
  }

  function confirmDelete() {
    if (deleteAccount.isPending) return;
    deleteAccount.mutate(undefined, {
      // Signed out: the layouts now send the user to /sign-in.
      onError: (error) => {
        const toTransfer = householdsToTransfer(error);
        if (toTransfer) {
          setMustTransfer(toTransfer);
          setDeleteDialogOpen(false);
        }
      },
    });
  }

  function openHouseholdSettings(householdId: string) {
    households.switchTo(householdId);
    router.navigate('/settings/household');
  }

  // Shown in the dialog; a must_transfer_ownership 409 is shown under the card instead.
  const deleteError =
    deleteAccount.isError && !householdsToTransfer(deleteAccount.error)
      ? deleteAccountErrorMessage(deleteAccount.error)
      : null;

  return (
    <>
      <NotificationsSection />
      <View testID="account-section" style={styles.section}>
        <Text role="heading" aria-level={2} style={styles.sectionTitle}>
          Account
        </Text>
        <View style={styles.card}>
          {editing ? (
            <View style={styles.editRow}>
              <TextField
                label="Name"
                testID="account-name-input"
                value={draft}
                onChangeText={setDraft}
                error={nameError}
                maxLength={NAME_MAX_LENGTH + 20}
                autoCapitalize="words"
                autoComplete="name"
                textContentType="name"
                returnKeyType="done"
                onSubmitEditing={saveName}
                autoFocus
              />
              {updateName.isError ? <FormMessage tone="error" message={userMessage(updateName.error)} /> : null}
              <View style={styles.editActions}>
                <Button label="Save" busy={updateName.isPending} onPress={saveName} />
                <Button label="Cancel" variant="secondary" disabled={updateName.isPending} onPress={() => setEditing(false)} />
              </View>
            </View>
          ) : (
            <Pressable
              testID="account-name"
              onPress={startEditing}
              disabled={!me.data}
              accessibilityRole="button"
              accessibilityLabel={me.data ? `Name, ${me.data.displayName}. Edit` : 'Name'}
              style={styles.row}
            >
              <Text style={styles.rowLabel}>Name</Text>
              {me.data ? (
                <Text testID="account-name-value" style={styles.rowValue}>
                  {me.data.displayName}
                </Text>
              ) : me.isError ? (
                <Text style={styles.rowProblem}>{userMessage(me.error)}</Text>
              ) : (
                <View style={styles.rowValue}>
                  <ActivityIndicator size="small" color={theme.colors.mutedText} />
                </View>
              )}
              {me.data ? <Text style={styles.rowAction}>Edit</Text> : null}
            </Pressable>
          )}
          <View style={[styles.row, styles.divider]}>
            <Text style={styles.rowLabel}>Email</Text>
            <Text testID="account-email" style={styles.rowValue}>
              {email ?? ''}
            </Text>
          </View>
          <Pressable
            onPress={handleSignOut}
            disabled={signingOut}
            accessibilityRole="button"
            accessibilityLabel="Sign out"
            accessibilityState={{ disabled: signingOut, busy: signingOut }}
            style={[styles.row, styles.divider]}
          >
            <Text style={styles.signOut}>Sign out</Text>
            {signingOut ? <ActivityIndicator size="small" color={theme.colors.mutedText} /> : null}
          </Pressable>
          <Pressable
            testID="delete-account"
            onPress={openDeleteDialog}
            disabled={signingOut || deleteAccount.isPending}
            accessibilityRole="button"
            accessibilityLabel="Delete account"
            style={[styles.row, styles.divider]}
          >
            <Text style={styles.deleteAccount}>Delete account</Text>
          </Pressable>
        </View>
        {signOutError ? <FormMessage tone="error" message={signOutError} /> : null}
        {mustTransfer ? (
          <FormMessage tone="error" message={transferOwnershipMessage(mustTransfer)} testID="must-transfer-ownership">
            {mustTransfer.map((household) => (
              <Button
                key={household.id}
                label={`Open ${household.name} settings`}
                variant="link"
                onPress={() => openHouseholdSettings(household.id)}
                testID={`transfer-household-${household.id}`}
              />
            ))}
          </FormMessage>
        ) : null}
      </View>
      <ConfirmDialog
        visible={deleteDialogOpen}
        testID="delete-account-dialog"
        title="Delete account"
        message={DELETE_ACCOUNT_MESSAGE}
        confirmLabel="Delete account"
        destructive
        busy={deleteAccount.isPending}
        error={deleteError}
        typeToConfirm={{ label: `Type "${DELETE_CONFIRM_WORD}" to confirm`, expected: DELETE_CONFIRM_WORD }}
        onConfirm={confirmDelete}
        onCancel={() => setDeleteDialogOpen(false)}
      />
    </>
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
      paddingHorizontal: theme.spacing.md + 2,
    },
    divider: { borderTopWidth: 1, borderTopColor: theme.colors.border },
    rowLabel: { ...theme.typography.body, width: 96, color: theme.colors.mutedText },
    rowValue: { ...theme.typography.body, flex: 1, color: theme.colors.text },
    rowProblem: { ...theme.typography.body, flex: 1, color: theme.colors.danger },
    rowAction: { ...theme.typography.label, color: theme.colors.primary },
    signOut: { ...theme.typography.label, fontSize: 16, flex: 1, color: theme.colors.text },
    deleteAccount: { ...theme.typography.label, fontSize: 16, flex: 1, color: theme.colors.danger },
    editRow: { gap: theme.spacing.md, padding: theme.spacing.md + 2 },
    editActions: { gap: theme.spacing.sm },
  };
}

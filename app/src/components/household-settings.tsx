// The household settings screen (/settings/household): the current
// household's name, members and invites, and what the user can do with them.
// Owner-only controls are only rendered for the owner, and the invites are only
// requested for the owner (a member would get 403).

import Feather from '@expo/vector-icons/Feather';
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { ActivityIndicator, Pressable, Text, View } from 'react-native';

import { userMessage } from '@/api/errors';
import { useMeQuery } from '@/auth/profile';
import { useCurrentHousehold } from '@/household/current-household';
import { HOUSEHOLD_NAME_MAX_LENGTH, validateHouseholdName } from '@/household/households';
import {
  formatInviteCode,
  inviteExpiryLabel,
  inviteShareMessage,
  isInviteActive,
  isOwnerOnlyError,
  useCreateInviteMutation,
  useDeleteHouseholdMutation,
  useInvitesQuery,
  useLeaveHouseholdMutation,
  useMembersQuery,
  useReloadAfterOwnershipLost,
  useRemoveMemberMutation,
  useRenameHouseholdMutation,
  useRevokeInviteMutation,
  useTransferOwnershipMutation,
  type Invite,
  type Member,
} from '@/household/settings';
import { copyText, shareInvite } from '@/household/share-invite';
import { MIN_TOUCH_TARGET, useTheme, useThemedStyles, type Theme } from '@/theme';

import { Button } from './button';
import { ConfirmDialog } from './confirm-dialog';
import { FormMessage } from './form-message';
import { HouseholdTile } from './household-switcher';
import { TextField } from './text-field';

/** How long "Copied" shows on a copy or share button, in ms. */
export const COPIED_DURATION_MS = 2000;

export const OWNER_LEAVE_HINT = 'Transfer ownership to another member before leaving, or delete the household';

export function ownershipLostMessage(name: string): string {
  return `You're no longer the owner of ${name}`;
}

export function memberLabel(member: Member, myUserId: string | undefined): string {
  return member.userId === myUserId ? `${member.displayName} (you)` : member.displayName;
}

type Dialog =
  | { kind: 'leave' }
  | { kind: 'remove'; member: Member }
  | { kind: 'transferPick' }
  | { kind: 'transferConfirm'; member: Member }
  | { kind: 'delete' }
  | { kind: 'revoke'; invite: Invite };

export function HouseholdSettings() {
  const styles = useThemedStyles(createStyles);
  const household = useCurrentHousehold();
  const isOwner = household.role === 'owner';
  const me = useMeQuery();
  const myUserId = me.data?.id;
  const members = useMembersQuery();
  const invites = useInvitesQuery(isOwner);
  const reloadAfterOwnershipLost = useReloadAfterOwnershipLost();

  // "You're no longer the owner of <name>", for the household it happened in.
  const [ownershipLost, setOwnershipLost] = useState<{ householdId: string; message: string } | null>(null);
  const handleOwnerOnly = useCallback(
    (error: unknown): boolean => {
      if (!isOwnerOnlyError(error)) return false;
      const lost = { householdId: household.id, message: ownershipLostMessage(household.name) };
      // Said once the household has reloaded, together with the member view.
      void reloadAfterOwnershipLost().finally(() => setOwnershipLost(lost));
      return true;
    },
    [household.id, household.name, reloadAfterOwnershipLost],
  );
  useEffect(() => {
    if (invites.isError) handleOwnerOnly(invites.error);
  }, [invites.isError, invites.error, handleOwnerOnly]);

  // The dialog stays filled in while it fades out.
  const [dialog, setDialog] = useState<Dialog | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);

  const leave = useLeaveHouseholdMutation();
  const remove = useRemoveMemberMutation();
  const transfer = useTransferOwnershipMutation();
  const deleteHousehold = useDeleteHouseholdMutation();
  const revoke = useRevokeInviteMutation();
  const [transferChoice, setTransferChoice] = useState<string | null>(null);

  const openDialog = useCallback(
    (next: Dialog) => {
      leave.reset();
      remove.reset();
      transfer.reset();
      deleteHousehold.reset();
      revoke.reset();
      if (next.kind === 'transferPick') setTransferChoice(null);
      setDialog(next);
      setDialogOpen(true);
    },
    [leave, remove, transfer, deleteHousehold, revoke],
  );
  const closeDialog = useCallback(() => setDialogOpen(false), []);

  /** Runs on a failed owner action: a 403 `owner_only` closes the dialog and reloads into the member view. */
  const onOwnerActionError = useCallback(
    (error: unknown) => {
      if (handleOwnerOnly(error)) closeDialog();
    },
    [handleOwnerOnly, closeDialog],
  );

  const memberList = members.data ?? [];
  const otherMembers = memberList.filter((member) => member.userId !== myUserId);
  const lostMessage = ownershipLost?.householdId === household.id ? ownershipLost.message : null;

  return (
    <View testID="household-settings" style={styles.root}>
      {lostMessage ? <FormMessage tone="info" message={lostMessage} testID="ownership-lost" /> : null}

      <HouseholdCard onOwnerOnly={handleOwnerOnly} />

      <Section title="Members">
        {members.data ? (
          <View style={styles.card} testID="member-list">
            {memberList.map((member, index) => (
              <MemberRow
                key={member.userId}
                member={member}
                myUserId={myUserId}
                first={index === 0}
                canRemove={isOwner && member.userId !== myUserId && member.role !== 'owner'}
                onRemove={() => openDialog({ kind: 'remove', member })}
              />
            ))}
          </View>
        ) : members.isError ? (
          <FormMessage tone="error" message={userMessage(members.error)} testID="members-error">
            <Button label="Try again" variant="link" busy={members.isFetching} onPress={() => void members.refetch()} />
          </FormMessage>
        ) : (
          <Loading label="Loading members" />
        )}
      </Section>

      {isOwner ? (
        <InvitesSection
          invites={invites.data}
          loading={invites.isPending}
          loadError={invites.isError && !isOwnerOnlyError(invites.error) ? invites.error : null}
          retry={() => void invites.refetch()}
          retrying={invites.isFetching}
          onOwnerOnly={handleOwnerOnly}
          onRevoke={(invite) => openDialog({ kind: 'revoke', invite })}
        />
      ) : null}

      {isOwner ? (
        <Section title="Owner actions">
          <View style={styles.card}>
            {members.data && otherMembers.length > 0 ? (
              <Pressable
                role="button"
                aria-label="Transfer ownership"
                testID="transfer-ownership"
                onPress={() => openDialog({ kind: 'transferPick' })}
                style={styles.actionRow}
              >
                <Text style={styles.actionLabel}>Transfer ownership</Text>
                <Text style={styles.caption}>Make another member the owner. You&apos;ll become a member.</Text>
              </Pressable>
            ) : members.data ? (
              <View style={styles.actionRow} testID="transfer-unavailable">
                <Text style={[styles.actionLabel, styles.disabledLabel]}>Transfer ownership</Text>
                <Text style={styles.caption}>
                  Someone must join {household.name} before you can transfer ownership. Invite them above.
                </Text>
              </View>
            ) : null}
            <Pressable
              role="button"
              aria-label="Delete household"
              testID="delete-household"
              onPress={() => openDialog({ kind: 'delete' })}
              style={[styles.actionRow, members.data ? styles.divider : null]}
            >
              <Text style={[styles.actionLabel, styles.dangerLabel]}>Delete household</Text>
              <Text style={styles.caption}>Deletes every pantry entry, for every member.</Text>
            </Pressable>
          </View>
        </Section>
      ) : null}

      <View style={styles.leave}>
        <Button
          label="Leave household"
          variant="secondary"
          testID="leave-household"
          disabled={isOwner || !myUserId}
          onPress={() => openDialog({ kind: 'leave' })}
        />
        {isOwner ? (
          <Text style={styles.caption} testID="owner-leave-hint">
            {OWNER_LEAVE_HINT}
          </Text>
        ) : null}
      </View>

      {dialog?.kind === 'leave' ? (
        <ConfirmDialog
          visible={dialogOpen}
          title="Leave household"
          message={`Leave ${household.name}? You'll lose access to its pantry.`}
          confirmLabel="Leave household"
          destructive
          busy={leave.isPending}
          error={leave.isError ? userMessage(leave.error) : null}
          onConfirm={() => myUserId && leave.mutate(myUserId)}
          onCancel={closeDialog}
        />
      ) : dialog?.kind === 'remove' ? (
        <ConfirmDialog
          visible={dialogOpen}
          title="Remove member"
          message={`Remove ${dialog.member.displayName} from ${household.name}? They'll lose access to its pantry.`}
          confirmLabel="Remove"
          destructive
          busy={remove.isPending}
          error={remove.isError ? userMessage(remove.error) : null}
          onConfirm={() =>
            remove.mutate(dialog.member.userId, { onSuccess: closeDialog, onError: onOwnerActionError })
          }
          onCancel={closeDialog}
        />
      ) : dialog?.kind === 'transferPick' ? (
        <ConfirmDialog
          visible={dialogOpen}
          title="Transfer ownership"
          message="Who should become the owner?"
          confirmLabel="Continue"
          confirmDisabled={!otherMembers.some((member) => member.userId === transferChoice)}
          onConfirm={() => {
            const member = otherMembers.find((item) => item.userId === transferChoice);
            if (member) setDialog({ kind: 'transferConfirm', member });
          }}
          onCancel={closeDialog}
        >
          <View role="radiogroup" aria-label="New owner" style={styles.card}>
            {otherMembers.map((member, index) => (
              <MemberChoice
                key={member.userId}
                member={member}
                first={index === 0}
                selected={member.userId === transferChoice}
                onSelect={() => setTransferChoice(member.userId)}
              />
            ))}
          </View>
        </ConfirmDialog>
      ) : dialog?.kind === 'transferConfirm' ? (
        <ConfirmDialog
          visible={dialogOpen}
          title="Transfer ownership"
          message={`Make ${dialog.member.displayName} the owner? You'll become a member.`}
          confirmLabel="Make owner"
          busy={transfer.isPending}
          error={transfer.isError ? userMessage(transfer.error) : null}
          onConfirm={() =>
            transfer.mutate(dialog.member.userId, { onSuccess: closeDialog, onError: onOwnerActionError })
          }
          onCancel={closeDialog}
        />
      ) : dialog?.kind === 'delete' ? (
        <ConfirmDialog
          visible={dialogOpen}
          title="Delete household"
          message={`This deletes ${household.name} and every pantry entry in it, for every member. It can't be undone.`}
          typeToConfirm={{ label: `Type "${household.name}" to confirm`, expected: household.name }}
          confirmLabel="Delete household"
          destructive
          busy={deleteHousehold.isPending}
          error={deleteHousehold.isError ? userMessage(deleteHousehold.error) : null}
          onConfirm={() => deleteHousehold.mutate(undefined, { onError: onOwnerActionError })}
          onCancel={closeDialog}
        />
      ) : dialog?.kind === 'revoke' ? (
        <ConfirmDialog
          visible={dialogOpen}
          title="Revoke invite"
          message={`Revoke the invite ${formatInviteCode(dialog.invite.code)}? Its code and link will stop working. People who already joined stay.`}
          confirmLabel="Revoke"
          destructive
          busy={revoke.isPending}
          error={revoke.isError ? userMessage(revoke.error) : null}
          onConfirm={() => revoke.mutate(dialog.invite.id, { onSuccess: closeDialog, onError: onOwnerActionError })}
          onCancel={closeDialog}
        />
      ) : null}
    </View>
  );
}

// --- Household name ------------------------------------------------------------------

function HouseholdCard({ onOwnerOnly }: { onOwnerOnly: (error: unknown) => boolean }) {
  const styles = useThemedStyles(createStyles);
  const household = useCurrentHousehold();
  const isOwner = household.role === 'owner';
  const rename = useRenameHouseholdMutation();
  // Editing stops when the household changes or the user is no longer the owner.
  const editKey = isOwner ? household.id : null;
  const [editingKey, setEditingKey] = useState<string | null>(null);
  const editing = editKey !== null && editingKey === editKey;
  const setEditing = (value: boolean) => setEditingKey(value ? editKey : null);
  const [draft, setDraft] = useState('');
  const [nameError, setNameError] = useState<string | null>(null);

  function startEditing() {
    setDraft(household.name);
    setNameError(null);
    rename.reset();
    setEditing(true);
  }

  function save() {
    if (rename.isPending) return;
    const error = validateHouseholdName(draft);
    setNameError(error);
    if (error) return;
    rename.mutate(draft, {
      onSuccess: () => setEditing(false),
      onError: (failure) => {
        if (onOwnerOnly(failure)) setEditing(false);
      },
    });
  }

  return (
    <View style={styles.card}>
      {editing ? (
        <View style={styles.editBody}>
          <TextField
            label="Household name"
            testID="household-rename-input"
            value={draft}
            onChangeText={setDraft}
            error={nameError}
            maxLength={HOUSEHOLD_NAME_MAX_LENGTH + 20}
            autoCapitalize="words"
            autoComplete="off"
            returnKeyType="done"
            onSubmitEditing={save}
            editable={!rename.isPending}
            autoFocus
          />
          {rename.isError && !isOwnerOnlyError(rename.error) ? (
            <FormMessage tone="error" message={userMessage(rename.error)} testID="rename-error" />
          ) : null}
          <View style={styles.buttons}>
            <Button label="Save" testID="household-rename-save" busy={rename.isPending} onPress={save} />
            <Button label="Cancel" variant="secondary" disabled={rename.isPending} onPress={() => setEditing(false)} />
          </View>
        </View>
      ) : (
        <View style={styles.householdRow}>
          <HouseholdTile name={household.name} size="large" />
          <View style={styles.rowText}>
            <Text testID="household-settings-name" style={styles.householdName}>
              {household.name}
            </Text>
            <Text style={styles.caption}>{isOwner ? "You're the owner" : "You're a member"}</Text>
          </View>
          {isOwner ? <Button label="Rename" variant="link" testID="household-rename" onPress={startEditing} /> : null}
        </View>
      )}
    </View>
  );
}

// --- Members -------------------------------------------------------------------------

type MemberRowProps = {
  member: Member;
  myUserId: string | undefined;
  first: boolean;
  canRemove: boolean;
  onRemove: () => void;
};

function MemberRow({ member, myUserId, first, canRemove, onRemove }: MemberRowProps) {
  const styles = useThemedStyles(createStyles);
  return (
    <View testID={`member-${member.userId}`} style={[styles.memberRow, first ? null : styles.divider]}>
      <View style={styles.rowText}>
        <View style={styles.nameLine}>
          <Text style={styles.memberName}>{memberLabel(member, myUserId)}</Text>
          {member.role === 'owner' ? (
            <View style={styles.badge} testID="owner-badge">
              <Text style={styles.badgeText}>Owner</Text>
            </View>
          ) : null}
        </View>
        {member.email ? <Text style={styles.caption}>{member.email}</Text> : null}
      </View>
      {canRemove ? (
        <Pressable
          role="button"
          aria-label={`Remove ${member.displayName}`}
          onPress={onRemove}
          style={styles.inlineAction}
        >
          <Text style={styles.dangerLabel}>Remove</Text>
        </Pressable>
      ) : null}
    </View>
  );
}

function MemberChoice({
  member,
  first,
  selected,
  onSelect,
}: {
  member: Member;
  first: boolean;
  selected: boolean;
  onSelect: () => void;
}) {
  const styles = useThemedStyles(createStyles);
  const { colors } = useTheme();
  return (
    <Pressable
      role="radio"
      aria-checked={selected}
      accessibilityState={{ checked: selected }}
      aria-label={member.displayName}
      onPress={onSelect}
      style={[styles.memberRow, first ? null : styles.divider, selected ? styles.selected : null]}
    >
      <Feather
        name={selected ? 'check-circle' : 'circle'}
        size={20}
        color={selected ? colors.primary : colors.mutedText}
        aria-hidden
        accessible={false}
      />
      <View style={styles.rowText}>
        <Text style={styles.memberName}>{member.displayName}</Text>
        {member.email ? <Text style={styles.caption}>{member.email}</Text> : null}
      </View>
    </Pressable>
  );
}

// --- Invites -------------------------------------------------------------------------

/**
 * The current time, updated each minute and when the next invite expires, so an
 * invite disappears at its `expiresAt` and the "Expires ..." labels stay right.
 */
function useNow(invites: readonly Invite[] | undefined): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const upcoming = (invites ?? [])
      .map((invite) => new Date(invite.expiresAt).getTime())
      .filter((time) => time > now);
    const nextExpiry = upcoming.length ? Math.min(...upcoming) : Infinity;
    const delay = Math.max(0, Math.min(60_000, nextExpiry - now + 1));
    const timer = setTimeout(() => setNow(Date.now()), delay);
    return () => clearTimeout(timer);
  }, [invites, now]);
  return now;
}

type InvitesSectionProps = {
  invites: Invite[] | undefined;
  loading: boolean;
  loadError: unknown;
  retry: () => void;
  retrying: boolean;
  onOwnerOnly: (error: unknown) => boolean;
  onRevoke: (invite: Invite) => void;
};

function InvitesSection({ invites, loading, loadError, retry, retrying, onOwnerOnly, onRevoke }: InvitesSectionProps) {
  const styles = useThemedStyles(createStyles);
  const household = useCurrentHousehold();
  const create = useCreateInviteMutation();
  const now = useNow(invites);
  const active = (invites ?? []).filter((invite) => isInviteActive(invite, now));

  return (
    <Section title="Invites">
      <Text style={styles.caption}>
        Anyone with an active code or link can join {household.name} until it expires. Only you, as owner, can create
        or revoke invites.
      </Text>
      {invites ? (
        active.length > 0 ? (
          <View style={styles.card} testID="invite-list">
            {active.map((invite, index) => (
              <InviteRow
                key={invite.id}
                invite={invite}
                householdName={household.name}
                now={now}
                first={index === 0}
                onRevoke={() => onRevoke(invite)}
              />
            ))}
          </View>
        ) : (
          <Text style={styles.caption} testID="no-invites">
            No active invites.
          </Text>
        )
      ) : loadError ? (
        <FormMessage tone="error" message={userMessage(loadError)} testID="invites-error">
          <Button label="Try again" variant="link" busy={retrying} onPress={retry} />
        </FormMessage>
      ) : loading ? (
        <Loading label="Loading invites" />
      ) : null}
      {create.isError && !isOwnerOnlyError(create.error) ? (
        <FormMessage tone="error" message={userMessage(create.error)} testID="create-invite-error" />
      ) : null}
      <Button
        label="Create invite"
        variant="secondary"
        testID="create-invite"
        busy={create.isPending}
        onPress={() => create.mutate(undefined, { onError: (error) => void onOwnerOnly(error) })}
      />
    </Section>
  );
}

type InviteRowProps = {
  invite: Invite;
  householdName: string;
  now: number;
  first: boolean;
  onRevoke: () => void;
};

type Feedback = { action: 'code' | 'link' | 'share'; ok: boolean } | null;

function InviteRow({ invite, householdName, now, first, onRevoke }: InviteRowProps) {
  const styles = useThemedStyles(createStyles);
  const code = formatInviteCode(invite.code);
  const [feedback, setFeedback] = useState<Feedback>(null);
  const [sharing, setSharing] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);

  function show(next: Feedback) {
    if (timer.current) clearTimeout(timer.current);
    setFeedback(next);
    timer.current = setTimeout(() => setFeedback(null), COPIED_DURATION_MS);
  }

  async function copy(action: 'code' | 'link') {
    const ok = await copyText(action === 'code' ? code : invite.url);
    show({ action, ok });
  }

  async function share() {
    if (sharing) return;
    setSharing(true);
    try {
      const result = await shareInvite(inviteShareMessage(householdName, invite), invite.url);
      if (result === 'copied') show({ action: 'share', ok: true });
      else if (result === 'failed') show({ action: 'share', ok: false });
    } finally {
      setSharing(false);
    }
  }

  const copiedLabel = (action: 'code' | 'link' | 'share', label: string) =>
    feedback?.action === action && feedback.ok ? 'Copied' : label;

  return (
    <View testID={`invite-${invite.id}`} style={[styles.inviteRow, first ? null : styles.divider]}>
      <View style={styles.inviteHeader}>
        <View style={styles.rowText}>
          <Text style={styles.inviteCode} aria-label={`Invite code ${code}`}>
            {code}
          </Text>
          <Text style={styles.caption}>{inviteExpiryLabel(invite.expiresAt, new Date(now))}</Text>
        </View>
        <Pressable role="button" aria-label={`Revoke ${code}`} onPress={onRevoke} style={styles.inlineAction}>
          <Text style={styles.dangerLabel}>Revoke</Text>
        </Pressable>
      </View>
      <View style={styles.inviteActions}>
        <SmallAction label={copiedLabel('code', 'Copy code')} accessibilityLabel={`Copy code ${code}`} onPress={() => void copy('code')} />
        <SmallAction label={copiedLabel('link', 'Copy link')} accessibilityLabel={`Copy link for ${code}`} onPress={() => void copy('link')} />
        <SmallAction
          label={copiedLabel('share', 'Share')}
          accessibilityLabel={`Share ${code}`}
          busy={sharing}
          onPress={() => void share()}
        />
      </View>
      {feedback ? (
        <Text role="status" aria-live="polite" style={feedback.ok ? styles.caption : styles.errorText}>
          {feedback.ok ? 'Copied' : "Couldn't copy. Select the code and copy it yourself."}
        </Text>
      ) : null}
    </View>
  );
}

function SmallAction({
  label,
  accessibilityLabel,
  busy = false,
  onPress,
}: {
  label: string;
  accessibilityLabel: string;
  busy?: boolean;
  onPress: () => void;
}) {
  const styles = useThemedStyles(createStyles);
  const { colors } = useTheme();
  return (
    <Pressable
      role="button"
      aria-label={accessibilityLabel}
      aria-busy={busy}
      accessibilityState={{ busy, disabled: busy }}
      disabled={busy}
      onPress={onPress}
      style={styles.smallAction}
    >
      {busy ? <ActivityIndicator size="small" color={colors.primary} /> : null}
      <Text style={styles.smallActionLabel}>{label}</Text>
    </Pressable>
  );
}

// --- Shared pieces --------------------------------------------------------------------

function Section({ title, children }: { title: string; children: ReactNode }) {
  const styles = useThemedStyles(createStyles);
  return (
    <View style={styles.section}>
      <Text role="heading" aria-level={2} style={styles.sectionTitle}>
        {title}
      </Text>
      {children}
    </View>
  );
}

function Loading({ label }: { label: string }) {
  const { colors } = useTheme();
  return (
    <View aria-busy accessibilityLabel={label}>
      <ActivityIndicator size="small" color={colors.mutedText} />
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    root: { gap: theme.spacing.xl },
    section: { gap: theme.spacing.sm },
    sectionTitle: { ...theme.typography.overline, color: theme.colors.mutedText, paddingHorizontal: theme.spacing.xs },
    card: {
      backgroundColor: theme.colors.surface,
      borderColor: theme.colors.border,
      borderWidth: 1,
      borderRadius: theme.radii.card,
      overflow: 'hidden' as const,
    },
    divider: { borderTopWidth: 1, borderTopColor: theme.colors.border },
    householdRow: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.md,
      padding: theme.spacing.md + 2,
    },
    householdName: { ...theme.typography.title, color: theme.colors.text },
    editBody: { gap: theme.spacing.md, padding: theme.spacing.md + 2 },
    buttons: { gap: theme.spacing.sm },
    rowText: { flex: 1, minWidth: 0, gap: 2 },
    caption: { ...theme.typography.caption, color: theme.colors.mutedText },
    errorText: { ...theme.typography.caption, color: theme.colors.danger },
    memberRow: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.md,
      minHeight: 60,
      paddingVertical: theme.spacing.sm,
      paddingLeft: theme.spacing.md + 2,
      paddingRight: theme.spacing.sm,
    },
    selected: { backgroundColor: theme.colors.primarySubtle },
    nameLine: { flexDirection: 'row' as const, alignItems: 'center' as const, flexWrap: 'wrap' as const, gap: theme.spacing.sm },
    memberName: { ...theme.typography.label, fontSize: 16, color: theme.colors.text },
    badge: {
      paddingHorizontal: theme.spacing.sm,
      paddingVertical: 2,
      borderRadius: theme.radii.pill,
      backgroundColor: theme.colors.primarySubtle,
    },
    badgeText: { ...theme.typography.caption, fontWeight: '600' as const, color: theme.colors.primary },
    inlineAction: {
      minHeight: MIN_TOUCH_TARGET,
      minWidth: MIN_TOUCH_TARGET,
      paddingHorizontal: theme.spacing.md,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
    },
    dangerLabel: { ...theme.typography.label, color: theme.colors.danger },
    disabledLabel: { color: theme.colors.mutedText },
    actionRow: {
      minHeight: 64,
      justifyContent: 'center' as const,
      gap: 2,
      paddingVertical: theme.spacing.sm + 2,
      paddingHorizontal: theme.spacing.md + 2,
    },
    actionLabel: { ...theme.typography.label, fontSize: 16, color: theme.colors.text },
    leave: { gap: theme.spacing.sm },
    inviteRow: { gap: theme.spacing.sm, paddingVertical: theme.spacing.sm + 2, paddingLeft: theme.spacing.md + 2, paddingRight: theme.spacing.sm },
    inviteHeader: { flexDirection: 'row' as const, alignItems: 'center' as const, gap: theme.spacing.md },
    inviteCode: { ...theme.typography.title, letterSpacing: 1.5, color: theme.colors.text },
    inviteActions: { flexDirection: 'row' as const, flexWrap: 'wrap' as const, gap: theme.spacing.sm, paddingRight: theme.spacing.sm },
    smallAction: {
      flexDirection: 'row' as const,
      alignItems: 'center' as const,
      justifyContent: 'center' as const,
      gap: theme.spacing.xs,
      minHeight: MIN_TOUCH_TARGET,
      paddingHorizontal: theme.spacing.md,
      borderWidth: 1,
      borderColor: theme.colors.border,
      borderRadius: theme.radii.md,
      backgroundColor: theme.colors.background,
    },
    smallActionLabel: { ...theme.typography.label, color: theme.colors.text },
  };
}

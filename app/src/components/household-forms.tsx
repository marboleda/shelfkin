// The create and join forms, shared by onboarding, the household switcher and
// the invite link screen (#33).

import { useRef, useState } from 'react';
import { View } from 'react-native';

import { isApiError, userMessage } from '@/api/errors';
import {
  HOUSEHOLD_NAME_MAX_LENGTH,
  joinErrorMessage,
  validateHouseholdName,
  validateInviteCode,
  type Household,
  type JoinInviteResult,
} from '@/household/households';
import { useCreateHouseholdMutation, useJoinHouseholdMutation } from '@/household/mutations';
import { useThemedStyles, type Theme } from '@/theme';

import { Button } from './button';
import { FormMessage } from './form-message';
import { TextField } from './text-field';

type CreateHouseholdFormProps = {
  /** Called after the household is created (and made current), if the form is still open. */
  onCreated?: (household: Household) => void;
  autoFocus?: boolean;
};

/** A household name and a Create button. The new household becomes current. */
export function CreateHouseholdForm({ onCreated, autoFocus = false }: CreateHouseholdFormProps) {
  const styles = useThemedStyles(createStyles);
  const create = useCreateHouseholdMutation();
  const [name, setName] = useState('');
  const [nameError, setNameError] = useState<string | null>(null);
  // Set synchronously, so a double tap before the next render still sends one request.
  const submitting = useRef(false);

  function submit() {
    if (submitting.current) return;
    const error = validateHouseholdName(name);
    setNameError(error);
    if (error) return;
    submitting.current = true;
    create.mutate(name, {
      onSuccess: (household) => onCreated?.(household),
      onError: (failure) => {
        if (isApiError(failure) && failure.code === 'invalid_name') {
          setNameError(`Use 1 to ${HOUSEHOLD_NAME_MAX_LENGTH} characters`);
        }
      },
      onSettled: () => {
        submitting.current = false;
      },
    });
  }

  const failed = create.isError && !(isApiError(create.error) && create.error.code === 'invalid_name');

  return (
    <View style={styles.form}>
      <TextField
        label="Household name"
        testID="household-name-input"
        placeholder="e.g. Home"
        value={name}
        onChangeText={setName}
        error={nameError}
        // Lets a longer name be typed, so the message explains the limit.
        maxLength={HOUSEHOLD_NAME_MAX_LENGTH + 20}
        autoCapitalize="words"
        autoComplete="off"
        returnKeyType="done"
        onSubmitEditing={submit}
        editable={!create.isPending}
        autoFocus={autoFocus}
      />
      {failed ? (
        <FormMessage tone="error" message={userMessage(create.error)} testID="create-household-error">
          <Button label="Try again" variant="link" onPress={submit} />
        </FormMessage>
      ) : null}
      <Button label="Create household" busy={create.isPending} onPress={submit} />
    </View>
  );
}

type JoinHouseholdFormProps = {
  /** Called after joining (the household is then current), if the form is still open. */
  onJoined?: (result: JoinInviteResult) => void;
  /** A code to start with, e.g. from an invite link (#33). */
  initialCode?: string;
  autoFocus?: boolean;
};

/** An invite code and a Join button. The household becomes current, with a message. */
export function JoinHouseholdForm({ onJoined, initialCode = '', autoFocus = false }: JoinHouseholdFormProps) {
  const styles = useThemedStyles(createStyles);
  const join = useJoinHouseholdMutation();
  const [code, setCode] = useState(initialCode.toUpperCase());
  const [codeError, setCodeError] = useState<string | null>(null);
  const submitting = useRef(false);

  function submit() {
    if (submitting.current) return;
    const error = validateInviteCode(code);
    setCodeError(error);
    if (error) return;
    submitting.current = true;
    join.mutate(code, {
      onSuccess: (result) => onJoined?.(result),
      onError: (failure) => setCodeError(joinErrorMessage(failure)),
      onSettled: () => {
        submitting.current = false;
      },
    });
  }

  return (
    <View style={styles.form}>
      <TextField
        label="Invite code"
        testID="invite-code-input"
        placeholder="K7QM-4XPA"
        value={code}
        // Capitalised as typed. Spaces and dashes are kept: the API ignores them.
        onChangeText={(text) => setCode(text.toUpperCase())}
        error={codeError}
        autoCapitalize="characters"
        autoCorrect={false}
        autoComplete="off"
        spellCheck={false}
        returnKeyType="go"
        onSubmitEditing={submit}
        editable={!join.isPending}
        autoFocus={autoFocus}
      />
      <Button label="Join household" variant="secondary" busy={join.isPending} onPress={submit} />
    </View>
  );
}

function createStyles(theme: Theme) {
  return {
    form: { gap: theme.spacing.md },
  };
}

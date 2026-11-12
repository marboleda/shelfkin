import { useGlobalSearchParams } from 'expo-router';
import { useRef, useState } from 'react';
import { Text, View, type TextInputInstance } from 'react-native';

import { authErrorMessage, firebaseErrorCode } from '@/auth/errors';
import { REDIRECT_PARAM } from '@/auth/redirect';
import { signUp } from '@/auth/session';
import { NAME_MAX_LENGTH, validateEmail, validateName, validateNewPassword } from '@/auth/validation';
import { AuthHeader } from '@/components/auth-header';
import { Button } from '@/components/button';
import { FormMessage } from '@/components/form-message';
import { PressableLink } from '@/components/pressable-link';
import { Screen } from '@/components/screen';
import { TextField } from '@/components/text-field';
import { useThemedStyles, type Theme } from '@/theme';

type FieldErrors = { name?: string | null; email?: string | null; password?: string | null };

export default function SignUpScreen() {
  const styles = useThemedStyles(createStyles);
  const params = useGlobalSearchParams<{ [REDIRECT_PARAM]?: string }>();
  const [name, setName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [emailInUse, setEmailInUse] = useState(false);
  const [busy, setBusy] = useState(false);
  const submitting = useRef(false);
  const emailRef = useRef<TextInputInstance>(null);
  const passwordRef = useRef<TextInputInstance>(null);

  const redirect = typeof params[REDIRECT_PARAM] === 'string' ? params[REDIRECT_PARAM] : undefined;
  const redirectParams = redirect ? { [REDIRECT_PARAM]: redirect } : {};

  async function submit() {
    if (submitting.current) return;
    const errors: FieldErrors = {
      name: validateName(name),
      email: validateEmail(email),
      password: validateNewPassword(password),
    };
    setFieldErrors(errors);
    if (errors.name || errors.email || errors.password) return;

    submitting.current = true;
    setBusy(true);
    setFormError(null);
    setEmailInUse(false);
    try {
      await signUp(name, email, password);
      // Signed in: the profile sync sends the name and time zone with PATCH /me,
      // and the (guest) layout moves on to the app.
    } catch (error) {
      setEmailInUse(firebaseErrorCode(error) === 'auth/email-already-in-use');
      setFormError(authErrorMessage(error));
      submitting.current = false;
      setBusy(false);
    }
  }

  return (
    <Screen title="Create account" accessory={<AuthHeader />}>
      {formError ? (
        <FormMessage testID="form-error" tone="error" message={formError}>
          {emailInUse ? (
            <PressableLink
              href={{ pathname: '/sign-in', params: { ...redirectParams, email: email.trim() } }}
              accessibilityRole="link"
              style={styles.inlineLink}
            >
              <Text style={styles.linkLabel}>Sign in instead</Text>
            </PressableLink>
          ) : null}
        </FormMessage>
      ) : null}

      <View style={styles.form}>
        <TextField
          label="Name"
          testID="name-input"
          value={name}
          onChangeText={setName}
          error={fieldErrors.name}
          placeholder="What your household calls you"
          maxLength={NAME_MAX_LENGTH + 20}
          autoCapitalize="words"
          autoComplete="name"
          textContentType="name"
          returnKeyType="next"
          onSubmitEditing={() => emailRef.current?.focus()}
        />
        <TextField
          ref={emailRef}
          label="Email"
          testID="email-input"
          value={email}
          onChangeText={setEmail}
          error={fieldErrors.email}
          placeholder="you@example.com"
          keyboardType="email-address"
          inputMode="email"
          autoCapitalize="none"
          autoCorrect={false}
          autoComplete="email"
          textContentType="username"
          returnKeyType="next"
          onSubmitEditing={() => passwordRef.current?.focus()}
        />
        <TextField
          ref={passwordRef}
          label="Password"
          testID="password-input"
          password
          value={password}
          onChangeText={setPassword}
          error={fieldErrors.password}
          placeholder="At least 8 characters"
          autoCapitalize="none"
          autoCorrect={false}
          autoComplete="new-password"
          textContentType="newPassword"
          returnKeyType="go"
          onSubmitEditing={submit}
        />
        <Button label="Create account" busy={busy} onPress={submit} />
      </View>

      <View style={styles.switchRow}>
        <Text style={styles.switchText}>Already have an account?</Text>
        <PressableLink
          href={{ pathname: '/sign-in', params: redirectParams }}
          accessibilityRole="link"
          style={styles.inlineLink}
        >
          <Text style={styles.linkLabel}>Sign in</Text>
        </PressableLink>
      </View>
    </Screen>
  );
}

function createStyles(theme: Theme) {
  return {
    form: { gap: theme.spacing.md },
    switchRow: {
      flexDirection: 'row' as const,
      flexWrap: 'wrap' as const,
      alignItems: 'center' as const,
      gap: theme.spacing.xs,
    },
    switchText: { ...theme.typography.body, color: theme.colors.mutedText },
    inlineLink: { alignSelf: 'flex-start' as const, paddingVertical: theme.spacing.sm },
    linkLabel: { ...theme.typography.label, color: theme.colors.primary },
  };
}

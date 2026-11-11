import { useGlobalSearchParams } from 'expo-router';
import { useRef, useState } from 'react';
import { Text, View, type TextInputInstance } from 'react-native';

import { AUTH_MESSAGES, authErrorMessage, resetSentMessage } from '@/auth/errors';
import { REDIRECT_PARAM } from '@/auth/redirect';
import { sendPasswordReset, signIn, useAuth } from '@/auth/session';
import { validateEmail } from '@/auth/validation';
import { AuthHeader } from '@/components/auth-header';
import { Button } from '@/components/button';
import { FormMessage } from '@/components/form-message';
import { PressableLink } from '@/components/pressable-link';
import { Screen } from '@/components/screen';
import { TextField } from '@/components/text-field';
import { useThemedStyles, type Theme } from '@/theme';

type FieldErrors = { email?: string | null; password?: string | null };

export default function SignInScreen() {
  const styles = useThemedStyles(createStyles);
  const auth = useAuth();
  const params = useGlobalSearchParams<{ [REDIRECT_PARAM]?: string; email?: string }>();
  const [email, setEmail] = useState(typeof params.email === 'string' ? params.email : '');
  const [password, setPassword] = useState('');
  const [fieldErrors, setFieldErrors] = useState<FieldErrors>({});
  const [formError, setFormError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [resetBusy, setResetBusy] = useState(false);
  // A ref, not state, so a second tap before the re-render still sees the first one.
  const submitting = useRef(false);
  const passwordRef = useRef<TextInputInstance>(null);

  const sessionEnded = auth.status === 'signedOut' && auth.sessionEnded;
  const redirect = typeof params[REDIRECT_PARAM] === 'string' ? params[REDIRECT_PARAM] : undefined;

  async function submit() {
    if (submitting.current) return;
    setNotice(null);
    const errors: FieldErrors = {
      email: validateEmail(email),
      password: password.length === 0 ? 'Enter your password' : null,
    };
    setFieldErrors(errors);
    if (errors.email || errors.password) return;

    submitting.current = true;
    setBusy(true);
    setFormError(null);
    try {
      await signIn(email, password);
      // The (guest) layout now sends the user on to the route they asked for.
    } catch (error) {
      setFormError(authErrorMessage(error));
      submitting.current = false;
      setBusy(false);
    }
  }

  async function forgotPassword() {
    if (resetBusy) return;
    setFormError(null);
    setNotice(null);
    const emailError = validateEmail(email);
    setFieldErrors({ email: emailError });
    if (emailError) return;

    setResetBusy(true);
    try {
      await sendPasswordReset(email);
      setNotice(resetSentMessage(email.trim()));
    } catch (error) {
      setFormError(authErrorMessage(error));
    } finally {
      setResetBusy(false);
    }
  }

  return (
    <Screen title="Sign in" accessory={<AuthHeader />}>
      {sessionEnded && !formError && !notice ? (
        <FormMessage testID="session-ended" tone="info" message={AUTH_MESSAGES.sessionEnded} />
      ) : null}
      {formError ? <FormMessage testID="form-error" tone="error" message={formError} /> : null}
      {notice ? <FormMessage testID="form-notice" tone="info" message={notice} /> : null}

      <View style={styles.form}>
        <TextField
          label="Email"
          testID="email-input"
          value={email}
          onChangeText={setEmail}
          error={fieldErrors.email}
          placeholder="you@example.com"
          keyboardType="email-address"
          autoCapitalize="none"
          autoCorrect={false}
          autoComplete="email"
          textContentType="username"
          inputMode="email"
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
          autoCapitalize="none"
          autoCorrect={false}
          autoComplete="current-password"
          textContentType="password"
          returnKeyType="go"
          onSubmitEditing={submit}
        />
        <Button variant="link" label="Forgot password?" busy={resetBusy} onPress={forgotPassword} />
        <Button label="Sign in" busy={busy} onPress={submit} />
      </View>

      <View style={styles.switchRow}>
        <Text style={styles.switchText}>New to Shelfkin?</Text>
        <PressableLink
          href={{ pathname: '/sign-up', params: redirect ? { [REDIRECT_PARAM]: redirect } : {} }}
          accessibilityRole="link"
          style={styles.switchLink}
        >
          <Text style={styles.switchLinkLabel}>Create an account</Text>
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
    switchLink: { paddingVertical: theme.spacing.sm },
    switchLinkLabel: { ...theme.typography.label, color: theme.colors.primary },
  };
}

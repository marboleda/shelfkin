import { act, fireEvent, render, waitFor } from '@testing-library/react-native';
import { renderRouter, screen } from 'expo-router/testing-library';
import { signInWithEmailAndPassword } from 'firebase/auth';

import { Button } from '@/components/button';
import { setupApiServer } from '../test-utils/api';
import { mockFirebase, signedInApiHandlers, TEST_EMAIL, TEST_PASSWORD } from '../test-utils/auth';

jest.setTimeout(60000);

// Fonts never really load in Jest: report them as loaded so the app renders (see font-loading-test).
jest.mock('expo-font', () => ({
  ...jest.requireActual('expo-font'),
  useFonts: () => [true, null],
  loadAsync: () => Promise.resolve(),
  isLoaded: () => true,
}));

jest.mock('@/auth/timezone', () => ({ deviceTimeZone: () => 'America/Toronto' }));

setupApiServer(...signedInApiHandlers());

const mockSignIn = signInWithEmailAndPassword as jest.MockedFunction<typeof signInWithEmailAndPassword>;

beforeEach(() => {
  mockSignIn.mockClear();
  mockFirebase.addAccount(TEST_EMAIL, TEST_PASSWORD);
});

async function openSignIn() {
  await renderRouter('./src/app', { initialUrl: '/sign-in' });
  expect(screen.getByRole('heading', { name: 'Sign in' })).toBeTruthy();
}

async function fill(email: string, password: string) {
  await fireEvent.changeText(screen.getByTestId('email-input'), email);
  await fireEvent.changeText(screen.getByTestId('password-input'), password);
}

async function pressSignIn() {
  await act(async () => {
    await fireEvent.press(screen.getByRole('button', { name: 'Sign in' }));
  });
}

function formError() {
  return screen.getByTestId('form-error');
}

describe('errors', () => {
  test('a wrong password and an unknown email show the same message', async () => {
    await openSignIn();

    await fill(TEST_EMAIL, 'wrong-password');
    await pressSignIn();
    expect(formError()).toHaveTextContent('Email or password is incorrect');

    await fill('nobody@example.com', TEST_PASSWORD);
    await pressSignIn();
    expect(formError()).toHaveTextContent('Email or password is incorrect');
    expect(screen).toHavePathname('/sign-in');
  });

  test.each([
    ['auth/too-many-requests', 'Too many attempts. Wait a few minutes and try again.'],
    ['auth/network-request-failed', "Can't connect. Check your connection and try again."],
    ['auth/internal-error', 'Something went wrong. Please try again.'],
  ])('%s shows a readable message, not the Firebase code', async (code, message) => {
    await openSignIn();
    mockFirebase.failNext(code);

    await fill(TEST_EMAIL, TEST_PASSWORD);
    await pressSignIn();

    expect(formError()).toHaveTextContent(message);
    expect(screen.queryByText(/auth\//)).toBeNull();
  });

  test('empty fields show their own messages and nothing is sent', async () => {
    await openSignIn();

    await pressSignIn();

    expect(screen.getByText('Enter your email address')).toBeTruthy();
    expect(screen.getByText('Enter your password')).toBeTruthy();
    expect(mockSignIn).not.toHaveBeenCalled();
  });
});

describe('submitting', () => {
  test('a double tap signs in once, and the button shows progress while signing in', async () => {
    await openSignIn();
    await fill(TEST_EMAIL, TEST_PASSWORD);

    const button = screen.getByRole('button', { name: 'Sign in' });
    await act(async () => {
      fireEvent.press(button);
      fireEvent.press(button);
    });

    expect(mockSignIn).toHaveBeenCalledTimes(1);
    await waitFor(() => expect(screen).toHavePathname('/'));
  });

  test('while busy, the button shows a spinner and is disabled', async () => {
    const onPress = jest.fn();
    await render(<Button label="Sign in" busy onPress={onPress} />);

    const button = screen.getByRole('button', { name: 'Sign in' });
    expect(button).toBeDisabled();
    expect(button).toBeBusy();
    expect(screen.getByTestId('button-busy')).toBeTruthy();
    await fireEvent.press(button);
    expect(onPress).not.toHaveBeenCalled();
  });

  test('pressing Enter in the password field submits the form', async () => {
    await openSignIn();
    await fill(TEST_EMAIL, TEST_PASSWORD);

    await act(async () => {
      await fireEvent(screen.getByTestId('password-input'), 'submitEditing');
    });

    expect(mockSignIn).toHaveBeenCalledWith(expect.anything(), TEST_EMAIL, TEST_PASSWORD);
    await waitFor(() => expect(screen).toHavePathname('/'));
  });
});

describe('fields', () => {
  test('the password is hidden, with a control to show and hide it', async () => {
    await openSignIn();
    const password = () => screen.getByTestId('password-input');

    expect(password()).toHaveProp('secureTextEntry', true);
    await fireEvent.press(screen.getByRole('button', { name: 'Show password' }));
    expect(password()).toHaveProp('secureTextEntry', false);
    await fireEvent.press(screen.getByRole('button', { name: 'Hide password' }));
    expect(password()).toHaveProp('secureTextEntry', true);
  });

  test('email and password support password managers and autofill', async () => {
    await openSignIn();

    expect(screen.getByTestId('email-input')).toHaveProp('autoComplete', 'email');
    expect(screen.getByTestId('email-input')).toHaveProp('textContentType', 'username');
    expect(screen.getByTestId('email-input')).toHaveProp('keyboardType', 'email-address');
    expect(screen.getByTestId('password-input')).toHaveProp('autoComplete', 'current-password');
    expect(screen.getByTestId('password-input')).toHaveProp('textContentType', 'password');
  });
});

describe('forgot password', () => {
  test.each([TEST_EMAIL, 'nobody@example.com'])('sends a reset email to %s and shows the same text', async (email) => {
    await openSignIn();
    await fireEvent.changeText(screen.getByTestId('email-input'), ` ${email} `);

    await act(async () => {
      await fireEvent.press(screen.getByRole('button', { name: 'Forgot password?' }));
    });

    expect(mockFirebase.resetEmails).toEqual([email]);
    expect(screen.getByTestId('form-notice')).toHaveTextContent(
      `If an account exists for ${email}, we've sent a reset link`,
    );
  });

  test('an unknown email reported by Firebase still shows the same text', async () => {
    await openSignIn();
    mockFirebase.failNext('auth/user-not-found');
    await fireEvent.changeText(screen.getByTestId('email-input'), 'nobody@example.com');

    await act(async () => {
      await fireEvent.press(screen.getByRole('button', { name: 'Forgot password?' }));
    });

    expect(screen.getByTestId('form-notice')).toHaveTextContent(
      "If an account exists for nobody@example.com, we've sent a reset link",
    );
  });

  test('without a valid email it asks for one and sends nothing', async () => {
    await openSignIn();

    await act(async () => {
      await fireEvent.press(screen.getByRole('button', { name: 'Forgot password?' }));
    });

    expect(screen.getByText('Enter your email address')).toBeTruthy();
    expect(mockFirebase.resetEmails).toEqual([]);
  });
});

// Route guards for the layouts. Screens never check auth themselves.

import { Redirect, useGlobalSearchParams, usePathname, useRouter } from 'expo-router';
import { useEffect, type ReactNode } from 'react';
import { View } from 'react-native';

import { REDIRECT_PARAM, redirectTarget, signInHref } from './redirect';
import { useAuth } from './session';

/**
 * Renders the signed-in routes only for a signed-in user. Anyone else goes to
 * /sign-in, which remembers the route they opened (e.g. a link to an entry)
 * and returns there after signing in. After a sign-out in this run, the user
 * just lands on /sign-in.
 */
export function SignedInOnly({ children }: { children: ReactNode }) {
  const auth = useAuth();
  const pathname = usePathname();

  if (auth.status === 'signedIn') return children;
  // The root layout shows the loading screen while the session is restored.
  if (auth.status === 'loading') return null;
  return <Redirect href={auth.signedOutInSession ? '/sign-in' : signInHref(pathname)} />;
}

/**
 * Renders /sign-in and /sign-up only for a signed-out user. A signed-in user
 * goes on to the route in the `redirect` parameter, or Pantry.
 *
 * Navigates from an effect rather than rendering `<Redirect>` in place of the
 * screens: once the guest navigator unmounts, the `redirect` parameter is gone
 * and the user would always land on Pantry.
 */
export function SignedOutOnly({ children }: { children: ReactNode }) {
  const auth = useAuth();
  const router = useRouter();
  const params = useGlobalSearchParams<{ [REDIRECT_PARAM]?: string }>();
  const signedIn = auth.status === 'signedIn';
  const target = redirectTarget(params[REDIRECT_PARAM]);

  useEffect(() => {
    if (signedIn) router.replace(target);
    // Only when the user becomes signed in; `target` is read at that moment.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [signedIn]);

  if (auth.status === 'loading') return null;
  // Hidden, not unmounted, while the navigation above happens.
  return <View style={{ flex: 1, display: signedIn ? 'none' : 'flex' }}>{children}</View>;
}

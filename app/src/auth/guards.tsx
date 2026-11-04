// Route guards for the layouts. Screens never check auth themselves.

import { Redirect, useGlobalSearchParams, usePathname } from 'expo-router';
import type { ReactNode } from 'react';

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

/** Renders /sign-in and /sign-up only for a signed-out user. A signed-in user goes on to the app. */
export function SignedOutOnly({ children }: { children: ReactNode }) {
  const auth = useAuth();
  const params = useGlobalSearchParams<{ [REDIRECT_PARAM]?: string }>();

  if (auth.status === 'signedIn') return <Redirect href={redirectTarget(params[REDIRECT_PARAM])} />;
  if (auth.status === 'loading') return null;
  return children;
}

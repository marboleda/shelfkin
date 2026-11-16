import { useLocalSearchParams } from 'expo-router';

import { SignedInForInvite } from '@/auth/guards';
import { IncompleteInviteScreen, JoinInviteScreen } from '@/components/join-invite';

/**
 * An invite link, https://<host>/join/<code> or shelfkin://join/<code> (#33).
 * Outside the (app) and (setup) layouts, so it works for users with or
 * without a household. Signed-out users sign in first and come back here.
 */
export default function JoinRoute() {
  const params = useLocalSearchParams<{ code?: string | string[] }>();
  const raw = Array.isArray(params.code) ? params.code[0] : params.code;
  const code = (raw ?? '').trim();

  if (!code) return <IncompleteInviteScreen />;
  return (
    // Keyed by code, so opening another invite link starts a new join.
    <SignedInForInvite code={code}>
      <JoinInviteScreen key={code} code={code} />
    </SignedInForInvite>
  );
}

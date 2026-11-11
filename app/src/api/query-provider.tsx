import { QueryClientProvider } from '@tanstack/react-query';
import { useEffect, useState, type ReactNode } from 'react';

import { onSignedOut } from '@/auth/session';

import { createQueryClient } from './query-client';

/**
 * Gives the app one shared `QueryClient`, created once when the root layout
 * mounts. Every sign-out empties it, so nothing from one user is shown to the
 * next person who signs in on the device.
 */
export function QueryProvider({ children }: { children: ReactNode }) {
  const [queryClient] = useState(() => createQueryClient());
  useEffect(() => onSignedOut(() => queryClient.clear()), [queryClient]);
  return <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>;
}

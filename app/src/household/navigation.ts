import { usePathname, useRouter } from 'expo-router';
import { useCallback, useEffect, useRef } from 'react';

import { useHouseholds, useOptionalCurrentHousehold } from './current-household';

/**
 * Screens that show one item of the current household (Add item, Entry
 * detail). After switching household they no longer apply, so the app goes to
 * Pantry for the new household.
 */
export function isHouseholdItemPath(pathname: string): boolean {
  return pathname === '/add' || pathname.startsWith('/add/') || pathname.startsWith('/entries/');
}

/**
 * Switches household, going to Pantry first when the user is on a screen for
 * one of the previous household's items. Choosing the current household does nothing.
 */
export function useSwitchHousehold(): (householdId: string) => void {
  const { switchTo } = useHouseholds();
  const current = useOptionalCurrentHousehold();
  const router = useRouter();
  const pathname = usePathname();
  const currentId = current?.id;

  return useCallback(
    (householdId: string) => {
      if (householdId === currentId) return;
      // Navigate in the same update as the switch, so Add item or Entry detail
      // is never shown under the new household's name.
      if (isHouseholdItemPath(pathname)) router.navigate('/');
      switchTo(householdId);
    },
    [currentId, pathname, router, switchTo],
  );
}

/**
 * Leaves Add item or Entry detail for Pantry whenever the current household
 * changes some other way: a household just created or joined, or a fallback
 * after losing access. Used once, in the signed-in shell's layout.
 */
export function useLeaveItemScreensOnHouseholdChange() {
  const current = useOptionalCurrentHousehold();
  const router = useRouter();
  const pathname = usePathname();
  const previousId = useRef(current?.id);

  useEffect(() => {
    const id = current?.id;
    if (previousId.current !== undefined && id !== undefined && id !== previousId.current && isHouseholdItemPath(pathname)) {
      router.navigate('/');
    }
    previousId.current = id;
    // Only when the household changes; `pathname` is read at that moment.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [current?.id]);
}

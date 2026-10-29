import { useWindowDimensions } from 'react-native';

import { isWideLayout } from '@/theme/layout';

/** True when the window is at least `WIDE_LAYOUT_BREAKPOINT` wide. Updates on resize/rotation. */
export function useIsWideLayout(): boolean {
  const { width } = useWindowDimensions();
  return isWideLayout(width);
}

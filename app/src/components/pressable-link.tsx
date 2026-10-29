import { Link, type Href } from 'expo-router';
import { Pressable, StyleSheet, type PressableProps, type StyleProp, type ViewStyle } from 'react-native';

type PressableLinkProps = Omit<PressableProps, 'style'> & {
  href: Href;
  /** A plain style (object or array). Pressable's `({ pressed }) => style` form is not supported. */
  style?: StyleProp<ViewStyle>;
};

/**
 * A `Pressable` that navigates to `href` (rendered as an `<a>` on web).
 *
 * `<Link asChild>` merges the child's style by spreading it, so a `style` function on the
 * child is silently dropped and a style array throws in development. This wrapper takes a
 * plain style and flattens it before handing it to the child.
 */
export function PressableLink({ href, style, ...rest }: PressableLinkProps) {
  return (
    <Link href={href} asChild>
      <Pressable {...rest} style={StyleSheet.flatten(style)} />
    </Link>
  );
}

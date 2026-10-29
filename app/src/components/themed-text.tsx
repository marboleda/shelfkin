import { Text, type TextProps } from 'react-native';

import { useThemedStyles, type Theme } from '@/theme';

type ThemedTextProps = TextProps & {
  variant?: 'body' | 'muted';
};

/** Body or muted text in the current theme's colours. */
export function ThemedText({ variant = 'body', style, ...rest }: ThemedTextProps) {
  const styles = useThemedStyles(createStyles);
  return <Text style={[styles[variant], style]} {...rest} />;
}

function createStyles(theme: Theme) {
  return {
    body: { ...theme.typography.body, color: theme.colors.text },
    muted: { ...theme.typography.body, color: theme.colors.mutedText },
  };
}

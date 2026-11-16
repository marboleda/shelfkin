import * as Clipboard from 'expo-clipboard';
import { Platform, Share } from 'react-native';

/** What happened when the user shared an invite. */
export type ShareResult = 'shared' | 'copied' | 'cancelled' | 'failed';

/** Puts `text` on the clipboard. Resolves to false when the browser refused. */
export async function copyText(text: string): Promise<boolean> {
  try {
    return await Clipboard.setStringAsync(text);
  } catch {
    return false;
  }
}

type WebShareNavigator = { share?: (data: { text?: string; url?: string; title?: string }) => Promise<void> };

function isAbort(error: unknown): boolean {
  return typeof error === 'object' && error !== null && (error as { name?: unknown }).name === 'AbortError';
}

/**
 * Shares an invite: the system share sheet on iOS and Android; on web the Web
 * Share API when the browser has it, otherwise the link is copied.
 */
export async function shareInvite(message: string, url: string): Promise<ShareResult> {
  if (Platform.OS === 'web') {
    const nav = (globalThis as { navigator?: WebShareNavigator }).navigator;
    if (typeof nav?.share === 'function') {
      try {
        await nav.share({ text: message });
        return 'shared';
      } catch (error) {
        if (isAbort(error)) return 'cancelled';
        // The browser refused (e.g. not allowed here): copy the link instead.
      }
    }
    return (await copyText(url)) ? 'copied' : 'failed';
  }
  try {
    const result = await Share.share({ message });
    return result.action === Share.dismissedAction ? 'cancelled' : 'shared';
  } catch {
    return 'failed';
  }
}

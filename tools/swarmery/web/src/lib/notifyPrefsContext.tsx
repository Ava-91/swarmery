// Notify-prefs context: the browser-notification preferences live in AppShell
// (which also mounts the `useBrowserNotifications` hook that rides the shared WS
// connection to fire background toasts). The header no longer hosts the controls
// — the global Settings page (/settings) does — so the prefs state is shared
// through this context: AppShell provides `{prefs, setPrefs}`, the Settings page
// consumes it to render <NotifySettings>. Keeping the state + hook in AppShell
// means relocating the UI never unmounts the notifier.

import { createContext, useContext } from 'react';
import { DEFAULT_PREFS, type NotifyPrefs } from './notifications';

interface NotifyPrefsValue {
  prefs: NotifyPrefs;
  setPrefs: (next: NotifyPrefs) => void;
}

export const NotifyPrefsContext = createContext<NotifyPrefsValue>({
  prefs: DEFAULT_PREFS,
  setPrefs: () => undefined,
});

/** Read the shared notification preferences + setter (Settings page). */
export function useNotifyPrefs(): NotifyPrefsValue {
  return useContext(NotifyPrefsContext);
}

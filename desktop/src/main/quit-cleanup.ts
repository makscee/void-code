export type QuitCleanupActions = {
  teardownSessions(): void;
  cleanupClipboardImages(): void;
  cleanupProbe(): void;
};

export function runQuitCleanup(actions: QuitCleanupActions): void {
  for (const cleanup of [actions.teardownSessions, actions.cleanupClipboardImages, actions.cleanupProbe]) {
    try { cleanup(); } catch { /* quitting continues after an owned cleanup failure */ }
  }
}

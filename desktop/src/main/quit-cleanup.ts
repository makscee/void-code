export type QuitCleanupActions = {
  teardownSessions(): void;
  cleanupClipboardImages(): void;
  cleanupProbe(): void;
};

export type OwnedCleanupCoordinator = {
  cleanup(): void;
};

export function runQuitCleanup(actions: QuitCleanupActions): void {
  for (const cleanup of [actions.teardownSessions, actions.cleanupClipboardImages, actions.cleanupProbe]) {
    try { cleanup(); } catch { /* quitting continues after an owned cleanup failure */ }
  }
}

// Electron can deliver before-quit, session-end, and an explicit app.exit in one lifecycle. Mark
// ownership released before running cleanup so a re-entrant lifecycle signal cannot tear down a
// PTY or remove an owned directory twice.
export function createOwnedCleanupCoordinator(actions: QuitCleanupActions): OwnedCleanupCoordinator {
  let cleaned = false;
  return {
    cleanup: () => {
      if (cleaned) return;
      cleaned = true;
      runQuitCleanup(actions);
    },
  };
}

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

// Electron can deliver before-quit, session-end, and an explicit app.exit in one lifecycle. Each
// owned action is complete only after it returns; failed actions remain eligible for a later
// lifecycle signal. The in-progress guard keeps a synchronous signal from re-entering any action.
export function createOwnedCleanupCoordinator(actions: QuitCleanupActions): OwnedCleanupCoordinator {
  const cleanups = [actions.teardownSessions, actions.cleanupClipboardImages, actions.cleanupProbe];
  const completed = cleanups.map(() => false);
  let cleaning = false;

  return {
    cleanup: () => {
      if (cleaning) return;
      cleaning = true;
      try {
        for (const [index, cleanup] of cleanups.entries()) {
          if (completed[index]) continue;
          try {
            cleanup();
            completed[index] = true;
          } catch { /* later owned cleanup actions must still run */ }
        }
      } finally {
        cleaning = false;
      }
    },
  };
}

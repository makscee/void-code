import type { AuthSpawner, LoginEvent } from './auth-session';
import { runLogin } from './auth-session';

export type AuthLoginPush =
  | ({ loginId: string } & LoginEvent)
  | { loginId: string; event: 'closed'; ok: true }
  | { loginId: string; event: 'closed'; ok: false; reason: string };

// runLogin ends in one of two ways: it resolves, or — if the spawner itself throws (a missing
// or unexecutable binary) — the Promise it returns rejects. Either way a login that stops must
// say so: a spinner with no closing push is indistinguishable from a login still in progress.
export function startAuthLogin(
  loginId: string,
  vcPath: string,
  spawn: AuthSpawner,
  deliver: (push: AuthLoginPush) => void,
  openUrl: (url: string) => void,
  onDiagnostic?: (message: string) => void,
): void {
  runLogin(vcPath, spawn, (event) => deliver({ loginId, ...event }), openUrl, onDiagnostic)
    .then((result) => {
      deliver(result.ok ? { loginId, event: 'closed', ok: true } : { loginId, event: 'closed', ok: false, reason: result.reason });
    })
    .catch(() => {
      // The raw spawn error (ENOENT, EACCES, ...) is a filesystem detail, not something the
      // renderer should see — it gets a stable word, same rule as auth-session's credential text.
      deliver({ loginId, event: 'closed', ok: false, reason: 'spawn_failed' });
    });
}

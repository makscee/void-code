import type { AuthStatus } from '../main/auth-session';

// The decision logic behind the sign-in button, kept out of index.ts on purpose: it has no DOM
// dependency, so it can be tested by calling it directly instead of pattern-matched as text.

export type AuthScreen = 'signed_in' | 'signed_out' | 'invalid_credential';

// An unreadable status (vc failed, malformed, or the read simply hasn't completed yet) must
// default to the screen that offers a sign-in action — never to signed_in, which nobody confirmed.
export function screenForStatus(status: AuthStatus | null): AuthScreen {
  if (status?.authState === 'signed_in') return 'signed_in';
  if (status?.authState === 'invalid_credential') return 'invalid_credential';
  return 'signed_out';
}

// expiresInSeconds is absent when vc doesn't send a lifetime — the code and URL are still real
// and actionable without it; only the countdown has nothing to show.
export interface CodePrompt { userCode: string; verificationUrl: string; expiresInSeconds?: number }

export type LoginPhase =
  | { phase: 'idle' }
  | ({ phase: 'code' } & CodePrompt)
  | { phase: 'authorized' }
  | { phase: 'closed_ok' }
  | { phase: 'error'; reason: string };

export type LoginPush =
  | ({ event: 'prompt' } & CodePrompt)
  | { event: 'authorized' }
  | { event: 'error'; reason: string }
  | { event: 'closed'; ok: true }
  | { event: 'closed'; ok: false; reason: string };

// auth-ipc's spawn_failed / exited_unexpectedly reasons arrive only on "closed" — there is no
// separate "error" push for them, so "closed" must itself be able to land on the error phase.
export function reduceLoginPush(current: LoginPhase, push: LoginPush): LoginPhase {
  switch (push.event) {
    case 'prompt': return { phase: 'code', userCode: push.userCode, verificationUrl: push.verificationUrl, expiresInSeconds: push.expiresInSeconds };
    case 'authorized': return { phase: 'authorized' };
    case 'error': return { phase: 'error', reason: push.reason };
    case 'closed': return push.ok ? { phase: 'closed_ok' } : { phase: 'error', reason: push.reason };
    default: return current;
  }
}

// "authorized" and a race-y "closed ok" both mean the login is done — but the push itself only
// carries vc's view. Whether the person is actually signed in has to come from a fresh status read.
export function requiresStatusRecheck(phase: LoginPhase): boolean {
  return phase.phase === 'authorized' || phase.phase === 'closed_ok';
}

// A code already on screen means a login is already in flight — a second click must not start a
// second one racing the first.
export function canStartLogin(phase: LoginPhase): boolean {
  return phase.phase !== 'code';
}

export type StartFailureRoute = { screen: 'generic' } | { screen: 'signin'; authScreen: AuthScreen };

// A chat that fails to start while signed in is a real fault — routing it to a login prompt would
// hide that fault behind a screen that has nothing to do with it. A chat that fails to start while
// signed out or credential-expired almost certainly failed *because* of that, and the generic
// "chat could not start" screen is the one screen that was never built for this exact person.
// Takes an already-fresh AuthScreen rather than reading status itself — the caller must re-check
// before routing, since the screen at the moment the chat was selected can be stale by the time it
// actually fails.
export function routeStartFailure(authScreen: AuthScreen): StartFailureRoute {
  return authScreen === 'signed_in' ? { screen: 'generic' } : { screen: 'signin', authScreen };
}

// undefined, not a fabricated 0/-1/Infinity, when the lifetime is unknown — a caller drawing a
// countdown must be forced to notice there is nothing to count down, not shown a number that
// means nothing.
export function codeSecondsRemaining(prompt: CodePrompt, elapsedSeconds: number): number | undefined {
  if (prompt.expiresInSeconds === undefined) return undefined;
  return Math.max(0, prompt.expiresInSeconds - elapsedSeconds);
}

// Never reports expired when there is no basis to say so — defaulting to "expired" would hide a
// still-usable code exactly the way the original defect hid the whole prompt.
export function isCodeExpired(prompt: CodePrompt, elapsedSeconds: number): boolean {
  if (prompt.expiresInSeconds === undefined) return false;
  return elapsedSeconds >= prompt.expiresInSeconds;
}

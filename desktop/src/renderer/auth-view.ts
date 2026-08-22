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
  | { phase: 'starting' }
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

// A code already on screen, or a login still starting (spawned but no push has arrived yet),
// both mean a login is already in flight — a second click must not start a second one racing it.
export function canStartLogin(phase: LoginPhase): boolean {
  return phase.phase !== 'code' && phase.phase !== 'starting';
}

// The click-triggered transition into the gap between the click and the first push from main —
// process spawn, then a network round trip for a device code. Without a distinct phase for that
// gap, a click looked identical to a dead button for however long that round trip takes. Safe to
// call unconditionally: it is itself a no-op (returns the same phase, unchanged) exactly when a
// login is already in flight, which is also when a second click must be ignored.
export function beginLogin(phase: LoginPhase): LoginPhase {
  return canStartLogin(phase) ? { phase: 'starting' } : phase;
}

// What the sign-in button itself says. "Signing in…" while starting is what makes a click that
// worked look different from a dead button, for however long the spawn and round trip take.
export function signInButtonLabel(phase: LoginPhase, authScreen: AuthScreen): string {
  if (phase.phase === 'starting') return 'Signing in…';
  return authScreen === 'invalid_credential' ? 'Sign in again' : 'Sign in';
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

// Stable machine words vc reports (or this codebase synthesises) for a login that did not
// succeed, turned into a sentence a person can act on. Every entry names the one thing this app
// can actually offer back: sign in again — except rate_limited, which must say the opposite of
// that for a while, or the retry itself makes the rate limit worse.
const LOGIN_FAILURE_MESSAGES: Record<string, string> = {
  rate_limited: 'Too many sign-in attempts right now — wait a few minutes, then try Sign in again.',
  start_failed: "Sign-in couldn't start. Check your network connection, then try Sign in again.",
  expired: 'This code expired before it was used. Click Sign in to get a new one.',
  denied: "Sign-in was denied. If that wasn't you, try Sign in again to be sure it's really you.",
  spawn_failed: "Void Code couldn't start the sign-in process. Try Sign in again — if it keeps happening, save a Support Report.",
  exited_unexpectedly: 'Sign-in stopped unexpectedly. Try Sign in again.',
};
// New reason words will arrive from vc before this file learns their names — echoing the raw
// word (e.g. "auth_backend_timeout_v3") would be no more actionable than the silence this
// feature replaces, so an unrecognised reason still gets a real sentence pointing at the one
// available action.
const UNKNOWN_LOGIN_FAILURE = "Sign-in didn't go through. Try Sign in again — if it keeps happening, save a Support Report.";

export function describeLoginFailure(reason: string): string {
  return LOGIN_FAILURE_MESSAGES[reason] ?? UNKNOWN_LOGIN_FAILURE;
}

// The one place that says what is going on right now, for the status line next to the button —
// distinct from the code screen's own countdown text, which only exists once a code is showing.
export function loginStatusText(phase: LoginPhase): string {
  switch (phase.phase) {
    case 'idle': return '';
    case 'starting': return 'Signing in…';
    case 'code': return 'Waiting for you to finish signing in.';
    case 'authorized':
    case 'closed_ok':
      return 'Signed in.';
    case 'error':
      return describeLoginFailure(phase.reason);
    default:
      return '';
  }
}

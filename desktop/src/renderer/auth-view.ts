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

export interface CodePrompt { userCode: string; verificationUrl: string; expiresInSeconds: number }

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

export function codeSecondsRemaining(prompt: CodePrompt, elapsedSeconds: number): number {
  return Math.max(0, prompt.expiresInSeconds - elapsedSeconds);
}

export function isCodeExpired(prompt: CodePrompt, elapsedSeconds: number): boolean {
  return elapsedSeconds >= prompt.expiresInSeconds;
}

import { describe, expect, it } from 'vitest';
import {
  canStartLogin, codeSecondsRemaining, isCodeExpired, reduceLoginPush, requiresStatusRecheck, routeStartFailure, screenForStatus,
  type LoginPhase,
} from '../src/renderer/auth-view';
import type { AuthStatus } from '../src/main/auth-session';

// This module carries the decision logic behind the sign-in button — which screen a person sees,
// and when a login attempt is allowed to start. It has no DOM dependency, so unlike index.ts it can
// be imported and driven directly instead of only grepped as text.

const prompt = { userCode: 'ABCD-1234', verificationUrl: 'https://auth.makscee.ru/device', expiresInSeconds: 900 } as const;

describe('screenForStatus — which of the three screens a signed-in-or-not person sees', () => {
  it('shows signed_in only for authState signed_in', () => {
    expect(screenForStatus({ authState: 'signed_in', identity: 'alex' } as AuthStatus)).toBe('signed_in');
  });

  it('never conflates a lost/expired credential with never having signed in', () => {
    // The whole point of this state: someone whose token expired must not land on the same
    // screen as someone who has never touched sign-in. A lazy implementation folding both into
    // one "not signed in" screen passes every other check but fails exactly this one.
    expect(screenForStatus({ authState: 'invalid_credential' } as AuthStatus)).toBe('invalid_credential');
    expect(screenForStatus({ authState: 'invalid_credential' } as AuthStatus)).not.toBe(screenForStatus({ authState: 'signed_out' } as AuthStatus));
  });

  it('shows signed_out for authState signed_out', () => {
    expect(screenForStatus({ authState: 'signed_out' } as AuthStatus)).toBe('signed_out');
  });

  it('treats an unreadable status (vc failed, malformed, or not yet loaded) as signed_out, never as signed_in', () => {
    // A status read can fail (StatusResult ok:false) or simply not have completed yet. Either way
    // the safe default is the screen that offers a sign-in action, not one that silently claims
    // the person is authenticated when nobody actually confirmed that.
    expect(screenForStatus(null)).toBe('signed_out');
  });
});

describe('reduceLoginPush — the state machine driving the code screen and its terminal outcomes', () => {
  const idle: LoginPhase = { phase: 'idle' };

  it('a prompt event shows the code, url and expiry — verbatim, not recomposed', () => {
    const next = reduceLoginPush(idle, { event: 'prompt', ...prompt });
    expect(next).toEqual({ phase: 'code', userCode: prompt.userCode, verificationUrl: prompt.verificationUrl, expiresInSeconds: prompt.expiresInSeconds });
  });

  it('authorized is a terminal outcome that lands on a defined phase, not a hang', () => {
    const codePhase = reduceLoginPush(idle, { event: 'prompt', ...prompt });
    const next = reduceLoginPush(codePhase, { event: 'authorized' });
    expect(next).toEqual({ phase: 'authorized' });
    expect(requiresStatusRecheck(next)).toBe(true);
  });

  it('an explicit error event is itself a terminal outcome — it does not wait for "closed" to become actionable', () => {
    const codePhase = reduceLoginPush(idle, { event: 'prompt', ...prompt });
    const next = reduceLoginPush(codePhase, { event: 'error', reason: 'denied' });
    expect(next).toEqual({ phase: 'error', reason: 'denied' });
  });

  it('closed ok:true (no explicit "authorized" seen, e.g. race) still asks for a status re-read', () => {
    const codePhase = reduceLoginPush(idle, { event: 'prompt', ...prompt });
    const next = reduceLoginPush(codePhase, { event: 'closed', ok: true });
    expect(next).toEqual({ phase: 'closed_ok' });
    expect(requiresStatusRecheck(next)).toBe(true);
  });

  it('closed ok:false lands on the error phase with its reason preserved, even with no prior error event', () => {
    // auth-ipc's spawn_failed / exited_unexpectedly reasons arrive only on "closed" — there is
    // no separate "error" push for them. The reducer must not require one to reach an actionable state.
    const codePhase = reduceLoginPush(idle, { event: 'prompt', ...prompt });
    const next = reduceLoginPush(codePhase, { event: 'closed', ok: false, reason: 'spawn_failed' });
    expect(next).toEqual({ phase: 'error', reason: 'spawn_failed' });
  });

  it('does not require a status re-read for the code or error phases themselves', () => {
    expect(requiresStatusRecheck({ phase: 'code', ...prompt })).toBe(false);
    expect(requiresStatusRecheck({ phase: 'error', reason: 'denied' })).toBe(false);
    expect(requiresStatusRecheck(idle)).toBe(false);
  });
});

describe('canStartLogin — clicking twice must not start two logins', () => {
  it('allows starting from idle and from every terminal phase', () => {
    expect(canStartLogin({ phase: 'idle' })).toBe(true);
    expect(canStartLogin({ phase: 'error', reason: 'denied' })).toBe(true);
    expect(canStartLogin({ phase: 'closed_ok' })).toBe(true);
    expect(canStartLogin({ phase: 'authorized' })).toBe(true);
  });

  it('refuses while a login is already in flight, i.e. once a code is on screen', () => {
    expect(canStartLogin({ phase: 'code', ...prompt })).toBe(false);
  });
});

describe('routeStartFailure — a chat that fails to start must re-check auth before choosing a screen', () => {
  // A returning person with an expired credential launches straight into `vc desktop-session`,
  // which refuses. Without this routing they land on the generic "chat could not start" screen —
  // the one screen built for their exact problem is the one they never see. routeStartFailure is
  // the decision itself, fed a *freshly re-read* AuthScreen by the caller; it does not read status.

  it('signed_in keeps the generic failure screen — a start failure while signed in is a real fault, not an auth problem', () => {
    // Routing everyone to sign-in on any start failure would hide genuine faults behind a login
    // prompt. That is the same defect in the other direction, and this case is what rejects it.
    expect(routeStartFailure('signed_in')).toEqual({ screen: 'generic' });
  });

  it('signed_out routes to the signed-out sign-in screen, not the generic failure', () => {
    expect(routeStartFailure('signed_out')).toEqual({ screen: 'signin', authScreen: 'signed_out' });
  });

  it('invalid_credential routes to its own screen — conflating it with signed_out would tell someone whose token merely expired to go through fresh sign-in copy that doesn\'t match their situation', () => {
    const route = routeStartFailure('invalid_credential');
    expect(route).toEqual({ screen: 'signin', authScreen: 'invalid_credential' });
    expect(route).not.toEqual(routeStartFailure('signed_out'));
  });
});

describe('codeSecondsRemaining / isCodeExpired — the code has to still be usable when someone reads it', () => {
  it('counts down and clamps at zero rather than going negative', () => {
    expect(codeSecondsRemaining(prompt, 0)).toBe(900);
    expect(codeSecondsRemaining(prompt, 400)).toBe(500);
    expect(codeSecondsRemaining(prompt, 900)).toBe(0);
    expect(codeSecondsRemaining(prompt, 1_000_000)).toBe(0);
  });

  it('is expired at and after the boundary, not before', () => {
    expect(isCodeExpired(prompt, 899)).toBe(false);
    expect(isCodeExpired(prompt, 900)).toBe(true);
    expect(isCodeExpired(prompt, 901)).toBe(true);
  });
});

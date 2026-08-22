import { describe, expect, it } from 'vitest';
import {
  beginLogin, canStartLogin, describeLoginFailure, loginStatusText, reduceLoginPush, requiresStatusRecheck,
  signInButtonLabel, type LoginPhase,
} from '../src/renderer/auth-view';

// A person who clicks "Sign in" and gets no visible reaction cannot tell a slow-but-working
// button from a dead one. Between the click and the first push from main (process spawn, then
// a network round trip to get a device code) the old LoginPhase union had nothing to be in —
// it was still 'idle', identical to having never clicked. These tests pin a real 'starting'
// phase for that gap, and the seams that read it: what the button says, whether a second click
// is allowed, and what the status line shows while nothing else has happened yet.

const prompt = { userCode: 'ABCD-1234', verificationUrl: 'https://auth.makscee.ru/device', expiresInSeconds: 900 } as const;

describe('beginLogin — the click-triggered transition into the gap before the first push arrives', () => {
  it('moves idle to starting', () => {
    expect(beginLogin({ phase: 'idle' })).toEqual({ phase: 'starting' });
  });

  it('moves every other startable phase to starting too, same as a fresh click from idle', () => {
    expect(beginLogin({ phase: 'error', reason: 'denied' })).toEqual({ phase: 'starting' });
    expect(beginLogin({ phase: 'closed_ok' })).toEqual({ phase: 'starting' });
    expect(beginLogin({ phase: 'authorized' })).toEqual({ phase: 'starting' });
  });

  it('refuses to re-enter starting from starting itself — returns the same phase unchanged', () => {
    // A second click while the spawn/round-trip is still in flight is exactly the double-start
    // this whole feature exists to prevent. beginLogin must be safe to call unconditionally.
    const starting: LoginPhase = { phase: 'starting' };
    expect(beginLogin(starting)).toBe(starting);
  });

  it('refuses to interrupt an in-flight code prompt — returns the same phase unchanged', () => {
    const codePhase: LoginPhase = { phase: 'code', ...prompt };
    expect(beginLogin(codePhase)).toBe(codePhase);
  });
});

describe('canStartLogin — starting counts as "a login is already in flight", same as code', () => {
  it('refuses while starting', () => {
    expect(canStartLogin({ phase: 'starting' })).toBe(false);
  });
});

describe('reduceLoginPush — a prompt arriving during starting lands on code, same as from idle', () => {
  it('the first push after beginLogin moves starting straight to code', () => {
    const starting: LoginPhase = { phase: 'starting' };
    const next = reduceLoginPush(starting, { event: 'prompt', ...prompt });
    expect(next).toEqual({ phase: 'code', userCode: prompt.userCode, verificationUrl: prompt.verificationUrl, expiresInSeconds: prompt.expiresInSeconds });
  });

  it('an error arriving during starting (spawn failed before any code was ever shown) lands on error, not on a stuck spinner', () => {
    const starting: LoginPhase = { phase: 'starting' };
    const next = reduceLoginPush(starting, { event: 'closed', ok: false, reason: 'spawn_failed' });
    expect(next).toEqual({ phase: 'error', reason: 'spawn_failed' });
  });
});

describe('requiresStatusRecheck — starting is not a terminal outcome', () => {
  it('does not ask for a status re-read while merely starting', () => {
    expect(requiresStatusRecheck({ phase: 'starting' })).toBe(false);
  });
});

describe('signInButtonLabel — what the button says, given the phase and which auth screen is showing', () => {
  it('reads "Signing in…" while starting, regardless of which screen is behind it', () => {
    expect(signInButtonLabel({ phase: 'starting' }, 'signed_out')).toBe('Signing in…');
    expect(signInButtonLabel({ phase: 'starting' }, 'invalid_credential')).toBe('Signing in…');
  });

  it('falls back to the existing signed_out/invalid_credential wording once not starting', () => {
    expect(signInButtonLabel({ phase: 'idle' }, 'signed_out')).toBe('Sign in');
    expect(signInButtonLabel({ phase: 'idle' }, 'invalid_credential')).toBe('Sign in again');
    expect(signInButtonLabel({ phase: 'error', reason: 'denied' }, 'invalid_credential')).toBe('Sign in again');
  });
});

// The reasons below are the stable words this codebase itself produces or passes through
// verbatim: 'denied' and 'expired' arrive from vc's own login --json 'error' event (passed
// through as free text in runLogin, src/main/auth-session.ts:126-128 — vc chooses the word,
// this app does not validate it against a known set); 'spawn_failed' and 'exited_unexpectedly'
// are synthesised by this codebase itself when the login child process never usefully starts
// or ends (src/main/auth-ipc.ts:27, src/main/auth-session.ts:147). 'rate_limited' and
// 'start_failed' are named explicitly in the report this feature responds to and in vc's own
// vocabulary for this class of failure, even though no fixture in this repo's tests has sent
// them yet — exactly the "arrives before this file learns about it" case the fallback below
// also has to cover, just pinned by name because we already know they are coming.
const KNOWN_REASONS = ['rate_limited', 'start_failed', 'expired', 'denied', 'spawn_failed', 'exited_unexpectedly'];

describe('describeLoginFailure — turning a stable machine word into a sentence a person can act on', () => {
  it('gives every known reason a distinct, non-empty sentence', () => {
    const messages = KNOWN_REASONS.map((reason) => describeLoginFailure(reason));
    for (const message of messages) expect(message.length).toBeGreaterThan(0);
    // Distinct wording matters: collapsing rate_limited and denied into the same sentence would
    // tell someone who was rate-limited to do the one thing (retry immediately) that makes a
    // rate limit worse.
    expect(new Set(messages).size).toBe(messages.length);
  });

  it('rate_limited specifically tells a person to wait and try again — not a generic failure', () => {
    // This is the exact word from the report: vc reported reason: "rate_limited" and it reached
    // the renderer with nowhere to go. "wait" rules out copy that only says "something went
    // wrong"; "sign in" (case-insensitive, matching the button's own label) rules out copy that
    // never mentions the way back.
    const message = describeLoginFailure('rate_limited');
    expect(message.toLowerCase()).toMatch(/wait/);
    expect(message.toLowerCase()).toMatch(/sign in/);
  });

  it('does not require automatic retry to be implied — the sentence for rate_limited must not promise the app will handle it on its own', () => {
    // A deliberate decision (see the task): the app must not auto-retry a rate limit, which
    // would make it worse. The copy must put the next action in the person's hands.
    const message = describeLoginFailure('rate_limited').toLowerCase();
    expect(message).not.toMatch(/automatically|we('| )ll retry|retrying/);
  });

  it('an unrecognised reason word still produces a readable sentence, not the raw word or an empty string', () => {
    // New words will appear from the Go side before this file learns about them (the report
    // says so explicitly). A lazy implementation that only handles the known list would either
    // throw, return undefined, or echo the raw machine word (e.g. "auth_backend_timeout_v3")
    // straight onto the screen — none of those are something a person can act on.
    const unknown = 'auth_backend_timeout_v3';
    const message = describeLoginFailure(unknown);
    expect(message.length).toBeGreaterThan(0);
    expect(message).not.toBe(unknown);
    expect(message).not.toContain(unknown);
  });

  it('the fallback for an unknown reason still points back at the one available action', () => {
    const message = describeLoginFailure('something_nobody_has_seen_yet').toLowerCase();
    expect(message).toMatch(/sign in/);
  });
});

describe('loginStatusText — the one place that says what is going on right now', () => {
  it('has nothing to say while idle', () => {
    expect(loginStatusText({ phase: 'idle' })).toBe('');
  });

  it('says sign-in is starting, distinct from the idle no-op text', () => {
    const text = loginStatusText({ phase: 'starting' });
    expect(text.length).toBeGreaterThan(0);
  });

  it('has something to say while a code is on screen, distinct from starting', () => {
    const codeText = loginStatusText({ phase: 'code', ...prompt });
    const startingText = loginStatusText({ phase: 'starting' });
    expect(codeText.length).toBeGreaterThan(0);
    expect(codeText).not.toBe(startingText);
  });

  it('says signed in for both authorized and the closed_ok race, and it is the same wording', () => {
    // Both mean "the login finished" from the person's point of view — the difference between
    // them is only which vc event happened to arrive, not anything worth showing on screen.
    expect(loginStatusText({ phase: 'authorized' })).toBe(loginStatusText({ phase: 'closed_ok' }));
    expect(loginStatusText({ phase: 'authorized' }).length).toBeGreaterThan(0);
  });

  it('delegates the error phase to describeLoginFailure rather than inventing separate copy', () => {
    expect(loginStatusText({ phase: 'error', reason: 'rate_limited' })).toBe(describeLoginFailure('rate_limited'));
    expect(loginStatusText({ phase: 'error', reason: 'some_new_reason' })).toBe(describeLoginFailure('some_new_reason'));
  });
});

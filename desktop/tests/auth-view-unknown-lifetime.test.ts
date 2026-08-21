import { describe, expect, it } from 'vitest';
import { codeSecondsRemaining, isCodeExpired, reduceLoginPush, type CodePrompt, type LoginPhase } from '../src/renderer/auth-view';

// auth-session.test.ts pins that a real vc line with no expiresInSeconds must still reach the
// renderer as a real prompt (userCode + verificationUrl), with the missing lifetime represented as
// a distinguishable absence rather than a fabricated number. This file pins the renderer side of
// that same decision: what a CodePrompt with no expiresInSeconds looks like once it's on screen.

const promptWithoutExpiry: CodePrompt = { userCode: 'DJAHWRAF', verificationUrl: 'https://auth.makscee.ru/device' };

describe('reduceLoginPush — a prompt push with no expiresInSeconds still reaches the code phase', () => {
  it('does not drop the prompt for a missing lifetime, and does not invent one', () => {
    const idle: LoginPhase = { phase: 'idle' };
    const next = reduceLoginPush(idle, { event: 'prompt', ...promptWithoutExpiry });
    expect(next).toEqual({ phase: 'code', userCode: 'DJAHWRAF', verificationUrl: 'https://auth.makscee.ru/device' });
    expect((next as { expiresInSeconds?: number }).expiresInSeconds).toBeUndefined();
  });
});

describe('codeSecondsRemaining with an unknown lifetime', () => {
  it('returns undefined rather than a number a countdown could render', () => {
    // A fabricated number (0, -1, Infinity) would still let a naive caller draw a countdown to a
    // moment that means nothing. undefined forces the caller to notice there is nothing to count down.
    expect(codeSecondsRemaining(promptWithoutExpiry, 0)).toBeUndefined();
    expect(codeSecondsRemaining(promptWithoutExpiry, 400)).toBeUndefined();
  });

  it('still counts down normally when the lifetime is known', () => {
    const promptWithExpiry: CodePrompt = { ...promptWithoutExpiry, expiresInSeconds: 900 };
    expect(codeSecondsRemaining(promptWithExpiry, 400)).toBe(500);
  });
});

describe('isCodeExpired with an unknown lifetime', () => {
  it('never reports expired when there is no basis to say so', () => {
    // Nobody confirmed this code is dead — defaulting to "expired" would hide a still-usable code
    // exactly the same way the original defect hid the whole prompt.
    expect(isCodeExpired(promptWithoutExpiry, 0)).toBe(false);
    expect(isCodeExpired(promptWithoutExpiry, 1_000_000)).toBe(false);
  });

  it('still expires normally when the lifetime is known', () => {
    const promptWithExpiry: CodePrompt = { ...promptWithoutExpiry, expiresInSeconds: 900 };
    expect(isCodeExpired(promptWithExpiry, 900)).toBe(true);
    expect(isCodeExpired(promptWithExpiry, 899)).toBe(false);
  });
});

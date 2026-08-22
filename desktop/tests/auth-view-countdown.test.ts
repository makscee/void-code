import { describe, expect, it } from 'vitest';
import { formatCountdown } from '../src/renderer/auth-view';

// The old copy printed the raw seconds ("Expires in 583s.") — not how anyone reads a countdown,
// and the owner's replacement design ("9:43 left") needs a pure function to get there. Kept as a
// standalone formatter, separate from codeSecondsRemaining (which decides *how many* seconds are
// left) and isCodeExpired (which decides whether to show a countdown at all), so the M:SS
// rendering can be pinned and tested without dragging in a prompt object or a clock.

describe('formatCountdown — turning a remaining-seconds count into "M:SS"', () => {
  it('formats a plain case the same shape as the owner\'s mock (9:43 left)', () => {
    expect(formatCountdown(583)).toBe('9:43');
  });

  it('zero-pads seconds under 10, but not minutes', () => {
    expect(formatCountdown(9)).toBe('0:09');
    expect(formatCountdown(60)).toBe('1:00');
    expect(formatCountdown(61)).toBe('1:01');
  });

  it('renders zero as 0:00, not an empty string or a negative-looking value', () => {
    // Zero is a real, reachable state (codeSecondsRemaining clamps to 0 once the code expires) —
    // it must still read as a clock, not disappear or wrap around.
    expect(formatCountdown(0)).toBe('0:00');
  });

  it('never prints a negative number, even if a caller passes one directly', () => {
    // codeSecondsRemaining already clamps at the call site, but formatCountdown is exported on
    // its own and must not trust every caller to clamp first — a raw negative reaching this
    // function is exactly the kind of defect a "pure formatter" is supposed to make impossible to
    // observe on screen.
    expect(formatCountdown(-1)).toBe('0:00');
    expect(formatCountdown(-900)).toBe('0:00');
  });

  it('keeps counting past an hour as minutes, not a broken or wrapped clock', () => {
    // The owner's mock only shows M:SS, never H:MM:SS — a lazy implementation could special-case
    // >=3600s into hour formatting nobody asked for, or silently wrap the minutes field. Neither
    // is acceptable: this must still just be "minutes:seconds" with minutes free to exceed 59.
    expect(formatCountdown(3600)).toBe('60:00');
    expect(formatCountdown(3661)).toBe('61:01');
  });

  it('floors a fractional number of seconds instead of rounding up past the real deadline', () => {
    // Rounding 59.9 up to 60 would claim a whole extra second exists when it doesn't.
    expect(formatCountdown(59.9)).toBe('0:59');
  });

  it('returns the empty string when the lifetime is unknown, never a fabricated clock', () => {
    // codeSecondsRemaining already returns undefined for a prompt with no expiresInSeconds
    // (auth-view-unknown-lifetime.test.ts). formatCountdown has to accept that undefined and
    // refuse to print anything that looks like a real countdown — "0:00" or "NaN:NaN" here would
    // both claim knowledge the app doesn't have, the exact defect codeSecondsRemaining/isCodeExpired
    // were already built to avoid one layer down.
    expect(formatCountdown(undefined)).toBe('');
  });
});

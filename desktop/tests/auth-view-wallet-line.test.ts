import { describe, expect, it } from 'vitest';
import type { AuthStatus } from '../src/main/auth-session';
import { walletLineFor } from '../src/renderer/auth-view';

// The balance and the days on the desktop — second panel on void-code#76, G3: auth-session passed
// the wallet and the launch notice through, and the renderer ignored both, so the desktop showed
// no money at all. The decision behind the line lives in auth-view, next to screenForStatus, for
// the reason stated at the top of that file: no DOM, so a test can call it.
//
// The rule:
//  - signed in → the line vc wrote (`walletText`, the exact formatWallet string), as is;
//  - anything else → null. A wallet line is an account fact, and only a signed-in status vouches
//    for the account;
//  - never composed here from the wallet object — the display rules (-$3.00, cents floored, days
//    never below 0) live in Go, once;
//  - never the launch notice: that warning stays inside Pi (docked above its editor), and showing
//    it here too would tell the person the same thing twice.
//
// The screen half (an element that shows this line) is pinned in tests/wallet-line-screen.test.ts.

const WALLET_TEXT = '$18.00 · T1 · ~9 days left';
const WALLET = { balanceUsd: 18, tariff: { tier: 't1', monthlyPriceUsd: 60, dailyRateUsd: 2 }, todayPaid: true, fundedDays: 9 };
const LOW_NOTICE = 'Balance low — 2 days left. Message @makscee on Telegram to top up.';

// Built as plain objects and cast: the status is what arrives over IPC, and the tests should not
// depend on how the AuthStatus type spells the field before the implementation lands.
const status = (fields: Record<string, unknown>): AuthStatus => fields as unknown as AuthStatus;

describe('walletLineFor', () => {
  it('is the line vc wrote, as is, for a signed-in status', () => {
    expect(walletLineFor(status({ authState: 'signed_in', identity: 'artem', wallet: WALLET, walletText: WALLET_TEXT }))).toBe(WALLET_TEXT);
  });

  it.each([
    ['a bare balance', '$18.00'],
    ['a debt with days clamped at zero', '-$3.00 · T1 · ~0 days left'],
    ['one day', '$2.00 · T1 · ~1 day left'],
  ])('passes %s through untouched', (_label, walletText) => {
    expect(walletLineFor(status({ authState: 'signed_in', walletText }))).toBe(walletText);
  });

  it('is null for a signed-in status without a line — no wallet, nothing to show', () => {
    expect(walletLineFor(status({ authState: 'signed_in', identity: 'artem' }))).toBeNull();
  });

  it('is null for an empty line, rather than a blank one', () => {
    expect(walletLineFor(status({ authState: 'signed_in', walletText: '' }))).toBeNull();
  });

  it('does not compose a line of its own from the wallet object', () => {
    // An older vc prints the wallet but no walletText. Formatting it here would be a second copy of
    // Go's rules, the first to drift.
    expect(walletLineFor(status({ authState: 'signed_in', wallet: WALLET }))).toBeNull();
  });

  it('never shows the launch notice — that one stays inside Pi', () => {
    expect(walletLineFor(status({ authState: 'signed_in', launchNotice: LOW_NOTICE }))).toBeNull();
    expect(walletLineFor(status({ authState: 'signed_in', walletText: WALLET_TEXT, launchNotice: LOW_NOTICE }))).toBe(WALLET_TEXT);
  });

  it.each(['signed_out', 'invalid_credential', 'access_not_granted', 'something_new'])(
    'is null for %s, even with a line riding along', (authState) => {
      expect(walletLineFor(status({ authState, walletText: WALLET_TEXT, wallet: WALLET }))).toBeNull();
    },
  );

  it('is null before the first status read has come back', () => {
    expect(walletLineFor(null)).toBeNull();
  });
});

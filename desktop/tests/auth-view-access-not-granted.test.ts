import { describe, expect, it } from 'vitest';
import * as authView from '../src/renderer/auth-view';
import type { AuthScreen } from '../src/renderer/auth-view';
import type { AuthStatus } from '../src/main/auth-session';

// The decision half of the fourth auth state. src/renderer/auth-view.ts has no DOM dependency, so
// unlike index.ts it is driven directly here; the markup and wiring live in
// tests/access-not-granted-screen.test.ts.
//
// What is wrong today: screenForStatus knows three words and funnels everything else into
// signed_out. A person whose sign-in worked but whose access was never granted is therefore shown
// "Sign in to start chatting". They sign in — it succeeds, because their credential was never the
// problem — and land back on the same screen. That loop is worse than yesterday's "your sign-in
// expired", because here the sign-in genuinely works, so no amount of retrying, and no diagnostic
// pointed at the login, will ever suggest the login is not the issue.

const { screenForStatus, routeStartFailure } = authView;

// offersSignIn does not exist yet. Reaching it through the namespace (rather than a named import,
// which would abort the whole module and make every test in this file fail with one unrelated
// message) is what keeps this failure attached to the tests that actually need the function.
function offersSignIn(screen: AuthScreen): boolean {
  const fn = (authView as Record<string, unknown>).offersSignIn;
  if (typeof fn !== 'function') {
    throw new Error('src/renderer/auth-view.ts does not export offersSignIn(screen) — the decision "may this screen offer a sign-in action" has nowhere to live outside index.ts');
  }
  return (fn as (screen: AuthScreen) => boolean)(screen);
}

const statusFor = (authState: string): AuthStatus => ({ authState } as unknown as AuthStatus);

describe('screenForStatus — access_not_granted is its own screen, not a fourth spelling of signed_out', () => {
  it('gives access_not_granted a screen of its own, and still falls back to signed_out for a word it does not know', () => {
    // The two halves are one test on purpose: the cheapest way to pass the first is
    // `return (status?.authState as AuthScreen) ?? 'signed_out'`, passing the word straight
    // through. That satisfies every access_not_granted assertion here while destroying the
    // whitelist — an unrecognised state would then reach the UI as a screen name that matches no
    // screen, and the person would be shown a panel with no sign-in action and no way out. The
    // fallback is deliberate protection, not an accident of the current shape.
    expect(screenForStatus(statusFor('access_not_granted'))).toBe('access_not_granted');

    expect(screenForStatus(statusFor('auth_backend_timeout_v3')), 'an unknown state stopped falling back to signed_out — the person is left on a screen with no way to sign in').toBe('signed_out');
    expect(screenForStatus(null), 'an unreadable status stopped falling back to signed_out').toBe('signed_out');
  });

  it('keeps all four screens distinct — most of all from invalid_credential, whose copy is a lie here', () => {
    // invalid_credential says "your sign-in has expired or was revoked. Sign in again". For this
    // person every word of that is false, and the instruction is the one action guaranteed to
    // waste their time. Folding the new state into any existing screen passes a "the state is
    // handled" review and reproduces the original defect exactly.
    const screens = ['signed_in', 'signed_out', 'invalid_credential', 'access_not_granted'].map((state) => screenForStatus(statusFor(state)));
    expect(new Set(screens).size, `four states collapsed into ${new Set(screens).size} screens: ${screens.join(', ')}`).toBe(4);
  });
});

describe('offersSignIn — the one screen that must not offer a sign-in', () => {
  it('withholds the sign-in action from the access screen, and still offers it to everyone who can use it', () => {
    // The button is not merely useless here, it is actively misleading: pressing it starts a
    // device flow that completes successfully, so the person watches the app confirm they signed
    // in and then return them to the same refusal. Every retry teaches them the wrong lesson.
    expect(offersSignIn('access_not_granted'), 'the access screen still offers a sign-in — it will succeed and change nothing').toBe(false);
    // Guarded here rather than left to the renderer, because the two states that still need the
    // action are the whole reason the button exists.
    expect(offersSignIn('signed_out')).toBe(true);
    expect(offersSignIn('invalid_credential')).toBe(true);
    expect(offersSignIn('signed_in')).toBe(false);
  });
});

describe('routeStartFailure — a chat that dies while access is not granted must land on the access screen', () => {
  it('routes the refusal to its own screen, not to the generic failure and not to a sign-in prompt', () => {
    // A returning person launches straight into a chat; `vc desktop-session` refuses because
    // access was never granted. Routed to 'generic' they get "chat could not start" — a fault
    // screen for a system that is working exactly as configured. Routed to signed_out they get
    // the sign-in loop again. The screen built for their situation is the one they must reach.
    // Fed through screenForStatus rather than a hand-written literal, because that is the path
    // index.ts actually takes: a screen name the mapper never produces is a screen nobody sees.
    const route = routeStartFailure(screenForStatus(statusFor('access_not_granted')));
    expect(route).toEqual({ screen: 'signin', authScreen: 'access_not_granted' });
    expect(route).not.toEqual(routeStartFailure('signed_out'));
    expect(route).not.toEqual(routeStartFailure('invalid_credential'));
  });
});

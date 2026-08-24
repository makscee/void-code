import type { AuthStatus } from '../main/auth-session';

// The decision logic behind the sign-in button, kept out of index.ts on purpose: it has no DOM
// dependency, so it can be tested by calling it directly instead of pattern-matched as text.

export type AuthScreen = 'signed_in' | 'signed_out' | 'invalid_credential' | 'access_not_granted';

// An unreadable status (vc failed, malformed, or the read simply hasn't completed yet) must
// default to the screen that offers a sign-in action — never to signed_in, which nobody confirmed,
// and never to access_not_granted, whose screen deliberately has no sign-in action at all: shown
// to someone who is merely signed out, it would leave them with no way back in.
// authState is read as a plain string rather than matched against main's union: this mapper is a
// whitelist of its own, so it keeps working the same way whether or not that union has yet learned
// a word vc has started printing — an unrecognised one still lands on signed_out.
export function screenForStatus(status: AuthStatus | null): AuthScreen {
  const authState: string | undefined = status?.authState;
  if (authState === 'signed_in') return 'signed_in';
  if (authState === 'invalid_credential') return 'invalid_credential';
  if (authState === 'access_not_granted') return 'access_not_granted';
  return 'signed_out';
}

// Whether the shared Sign in button belongs on a given screen. Lives here, next to the mapper that
// produces the screens, so the rule is testable without a DOM and so a fifth screen has exactly one
// place to declare its answer — inline in the renderer, the default for anything new would silently
// be "yes". "No" for access_not_granted is the point: that sign-in succeeds and changes nothing,
// which teaches the person the wrong lesson every time they press it.
export function offersSignIn(screen: AuthScreen): boolean {
  return screen === 'signed_out' || screen === 'invalid_credential';
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

// "M:SS", the shape a person actually reads as a countdown — not the raw seconds count
// codeSecondsRemaining hands back. Blank, not a fabricated "0:00" clock, when the lifetime is
// unknown: that is a separate real state (codeSecondsRemaining already returns undefined for
// it) and this function must not paper over it with a clock that means nothing.
export function formatCountdown(seconds: number | undefined): string {
  if (seconds === undefined) return '';
  const clamped = Math.max(0, Math.floor(seconds));
  const minutes = Math.floor(clamped / 60);
  const remainingSeconds = clamped % 60;
  return `${minutes}:${String(remainingSeconds).padStart(2, '0')}`;
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

// The status line under the button, and the only phase that has anything to put there is 'error'.
// Every other phase is already stated by something the person is looking at: the button itself
// says "Signing in…" while starting, the code screen carries its own countdown, and after a
// successful login the screen behind the button re-reads status and says "You're signed in" —
// so a second line repeating any of them just made one panel read as several rows of the same
// sentence. A failure has no such other place to appear, which is why it keeps this line.
export function loginStatusText(phase: LoginPhase): string {
  return phase.phase === 'error' ? describeLoginFailure(phase.reason) : '';
}

// The decision behind the "Request access" button and the line beside it. It lives here for the
// reason stated at the top of this file: no DOM dependency, so it can be called by a test instead
// of pattern-matched as text. Written inline in renderAuthScreens it would be a rule no unit test
// can reach, and the next state added would silently get whichever answer the else branch gives.
//
// Structural, not imported from main/access-request: `state` is read as a plain string so this
// mapper keeps its own whitelist — a word that module has not yet learned still lands on
// 'unknown' here rather than falling through as a request state.
export type AccessRequestOutcome =
  | { ok: true; report: { state: string; requestedAt?: string; resolvedAt?: string } }
  | { ok: false; reason: string };
export type AccessRequestKind = 'pending' | 'not_requested' | 'open' | 'declined' | 'granted' | 'unknown';
export interface AccessRequestView { kind: AccessRequestKind; canAsk: boolean; text: string }

// Everything that means "we did not get an answer" — the three words vc uses for it, plus every
// way the run itself can fail before vc says anything — arrives here, and it is deliberately not
// the same answer as "nothing has been filed". vc splits those outcomes on purpose. Collapsing
// them tells a person whose relay is down that they never asked: they press the button, it fails
// the same silent way, and the only conclusion left to them is that they are doing it wrong.
const COULD_NOT_ASK: AccessRequestView = {
  kind: 'unknown',
  canAsk: false,
  text: "Void Code couldn't check on the request just now. Check again in a moment.",
};

// A date the answer actually carried, or nothing at all. `new Date(undefined).toLocaleDateString()`
// is "Invalid Date" and it renders — it reads as a bug in the app rather than as a missing field,
// and it displaces the one sentence the person needed. Every caller below has a dateless sentence
// ready for that case.
function requestDate(value: string | undefined): string | undefined {
  if (typeof value !== 'string' || value.trim() === '') return undefined;
  const when = new Date(value);
  if (Number.isNaN(when.getTime())) return undefined;
  return when.toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' });
}

export function describeAccessRequest(result: AccessRequestOutcome | null): AccessRequestView {
  // The gap between the screen appearing and the read returning. Both wrong answers are on the
  // table: "nothing filed" invites a press that duplicates a request that already exists, and
  // "we could not ask" reports a failure that has not happened. So it claims neither.
  if (result === null) return { kind: 'pending', canAsk: false, text: '' };
  if (!result.ok) return COULD_NOT_ASK;
  const filed = requestDate(result.report.requestedAt);
  const answered = requestDate(result.report.resolvedAt);
  switch (result.report.state) {
    // The one state that offers the button. Every other one either has a request already — a
    // second press is another row in the queue for one person — or has an answer.
    case 'not_requested':
      return { kind: 'not_requested', canAsk: true, text: 'No request has been filed from this computer yet.' };
    case 'open':
      return {
        kind: 'open',
        canAsk: false,
        text: filed ? `Your request was filed on ${filed} and is waiting for an answer.` : 'Your request is filed and waiting for an answer.',
      };
    // The state a screen is most likely to swallow: nothing has to change visually for the code
    // to "work", and a person left on an unchanged screen goes on waiting for an answer that
    // already came.
    case 'declined':
      return {
        kind: 'declined',
        canAsk: false,
        text: answered ? `Your request was declined on ${answered}.` : 'Your request was declined.',
      };
    case 'granted':
      return {
        kind: 'granted',
        canAsk: false,
        text: answered ? `Your request was granted on ${answered}. Check again to continue.` : 'Your request was granted. Check again to continue.',
      };
    default:
      return COULD_NOT_ASK;
  }
}

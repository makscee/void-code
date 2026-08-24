import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// The screen half of the fourth auth state; the decisions behind it are exercised for real in
// tests/auth-view-access-not-granted.test.ts, and the status read that has to deliver the state at
// all in tests/auth-status-access-not-granted.test.ts.
//
// Same approach as tests/renderer-login.test.ts and tests/signin-code-screen.test.ts: index.ts has
// DOM side effects on import and there is no jsdom/happy-dom environment configured here, so the
// screen is pinned as text against the markup and the source that wires it. Honest limit: this
// proves index.html carries the structure and index.ts wires the right elements to the right
// calls; it cannot prove the panel renders, that the button's click reaches it, or that anyone can
// read the result. That needs a real Electron run, which nothing in this suite has.
//
// What the person is in the middle of: they pressed Sign in, the device flow completed, vc holds a
// working token — and the verifying service answered "access has not been granted to this account
// yet". Today the desktop has no screen for that, so they are shown "Sign in to start chatting"
// and the loop closes.

const html = readFileSync(new URL('../src/renderer/index.html', import.meta.url), 'utf8');
const renderer = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');

const SECTION_ID = 'signin-no-access';
const RECHECK_ID = 'signin-no-access-recheck';

// The screen's own markup: everything from its container up to the next sibling in #signin-panel
// (another signin-* div, or the shared Sign in button when it is placed last). Falls back to ''
// so every assertion fails with its own message rather than a cryptic regex miss.
const section = html.match(new RegExp(`<div id="${SECTION_ID}"[^>]*>[\\s\\S]*?</div>(?=\\s*<(?:div id="signin-|button id="signin-start|p id="signin-status))`))?.[0] ?? '';
// Copy as a person reads it: tags stripped, so ids and attribute values (which contain "signin",
// "button", "hidden") cannot satisfy or trip a check on the words on screen.
const sectionText = section.replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim();

// The renderer's binding for an id, resolved from the source rather than assumed — this file pins
// the ids it invented, not the variable names the implementer picks for them.
const bindingFor = (id: string): string =>
  renderer.match(new RegExp(`const (\\w+)\\s*=\\s*document\\.querySelector<[^>]*>\\('#${id}'\\)`))?.[1] ?? '';

const renderFn = renderer.match(/function renderAuthScreens\(\): void \{[\s\S]{0,3000}?\n\}/)?.[0] ?? '';

const clickHandler = (binding: string): string =>
  binding === '' ? '' : renderer.match(new RegExp(`${binding}\\.addEventListener\\('click',[\\s\\S]{0,600}?\\n\\}\\);`))?.[0] ?? '';

describe('the access screen exists as its own structure, not the expired-credential screen with a new heading', () => {
  it(`index.html carries a #${SECTION_ID} container, shipped hidden like its siblings`, () => {
    expect(section, `index.html has no #${SECTION_ID} container — there is no screen for a person whose sign-in worked and whose access was not granted`).not.toBe('');
    const openTag = section.match(/<div[^>]*>/)?.[0] ?? '';
    expect(openTag, `#${SECTION_ID} does not ship hidden, so it would be on screen for everyone`).toMatch(/\bhidden\b/);
  });

  it('the state gets that container of its own, instead of #signin-invalid being rewritten at runtime', () => {
    // The one-line version of this feature: leave the markup alone, and when the state is
    // access_not_granted overwrite the expired-credential panel's text. It passes a review that
    // only asks "does the new state show something different", and it leaves two states sharing
    // one element, one set of styles and one heading — the defect this whole file is about, moved
    // one level down.
    expect(section, `index.html has no #${SECTION_ID} container, so whatever this state shows today can only be another screen's element`).not.toBe('');
    const invalidBinding = bindingFor('signin-invalid');
    expect(invalidBinding, 'could not locate the #signin-invalid binding in index.ts').not.toBe('');
    expect(renderer, `index.ts rewrites ${invalidBinding}'s text at runtime — two auth states are sharing one panel`).not.toMatch(new RegExp(`${invalidBinding}\\.(?:textContent|innerHTML)\\s*=`));
    const signedOutBinding = bindingFor('signin-signed-out');
    expect(renderer, `index.ts rewrites ${signedOutBinding}'s text at runtime — two auth states are sharing one panel`).not.toMatch(new RegExp(`${signedOutBinding}\\.(?:textContent|innerHTML)\\s*=`));
  });

  it('renderAuthScreens shows it for access_not_granted and for nothing else', () => {
    const binding = bindingFor(SECTION_ID);
    expect(binding, `index.ts never selects #${SECTION_ID}, so the markup is dead weight`).not.toBe('');
    expect(renderFn, 'could not locate renderAuthScreens()').not.toBe('');
    // Keyed on the screen name the mapper actually produces. A branch on `status.authState`
    // read somewhere else, or on a fresh boolean flag, would drift from screenForStatus the first
    // time either side changes — the siblings on lines 75-78 are all keyed the same way.
    expect(renderFn, `renderAuthScreens does not toggle ${binding}.hidden on the access_not_granted screen`)
      .toMatch(new RegExp(`${binding}\\.hidden\\s*=[^;]*access_not_granted`));
    // The device-code screen owns the panel while a code is up; every sibling already yields to it.
    expect(renderFn, `${binding} stays on screen underneath the device-code screen`)
      .toMatch(new RegExp(`${binding}\\.hidden\\s*=[^;]*showingCode`));
  });
});

describe('the screen does not offer the one action that is guaranteed not to help', () => {
  it('the sign-in button is withheld through offersSignIn, not by a second copy of the decision inline', () => {
    expect(renderFn, 'could not locate renderAuthScreens()').not.toBe('');
    // `signinStartButton.hidden = authScreen === 'signed_in' || authScreen === 'access_not_granted' || showingCode`
    // is the tempting one-liner. It works, and it puts the rule in a place auth-view.test.ts
    // cannot reach — so the next state added to the union silently gets a sign-in button again,
    // and no unit test notices. The decision belongs in the pure module that is actually tested.
    expect(renderFn, 'signinStartButton.hidden does not go through offersSignIn(authScreen) — the rule lives inline, where nothing can test it')
      .toMatch(/signinStartButton\.hidden\s*=[^;]*offersSignIn\(/);
    expect(renderer, 'index.ts does not import offersSignIn from auth-view').toContain('offersSignIn');
  });

  it('nothing on this screen is labelled as a sign-in', () => {
    expect(section, `index.html has no #${SECTION_ID} container`).not.toBe('');
    // Covers a private "Sign in" / "Sign in again" button placed inside the panel — hiding the
    // shared button above and adding a local one under a different id is the same dead end with
    // more markup. Narrowed to the imperative form the buttons in this app use ("Sign in"), so
    // that describing what already happened ("your sign-in worked", "signing in again won't
    // change this") stays available: naming it is how the copy tells the person the loop is not
    // theirs to break.
    expect(sectionText, `#${SECTION_ID} offers a sign-in action: "${sectionText}"`).not.toMatch(/\bsign in\b/i);
  });
});

describe('the screen gives the person something real to do, and it is not a button that does nothing', () => {
  it(`carries a #${RECHECK_ID} button — the only honest action available: ask again`, () => {
    // Without it this screen is terminal. Access is granted elsewhere, on someone else's clock,
    // and the desktop reads status exactly twice: at startup and after a login resolves. Once a
    // person is parked here, the app has no reason left to look again, so the only way to
    // discover that access arrived is to quit and reopen — which nothing on screen would tell
    // them to do. Re-reading status is a real action with a real effect, and it is the only one
    // this client can honestly offer: the consumer-facing request handles in void-keys are
    // reachable only by a trusted subject through Relay, and no such proxying exists, so a
    // "request access" button here would be a button that does nothing.
    expect(section, `index.html has no #${SECTION_ID} container`).not.toBe('');
    const button = section.match(new RegExp(`<button[^>]*id="${RECHECK_ID}"[^>]*>([^<]*)</button>`));
    expect(button, `#${SECTION_ID} has no #${RECHECK_ID} button — the screen is a dead end until the app is restarted`).not.toBeNull();
    expect(button![0]).toMatch(/type="button"/);
    expect(button![1].trim(), 'the button has no label').not.toBe('');
  });

  it('the button actually re-reads auth status, exactly once per click, and never starts a login', () => {
    const binding = bindingFor(RECHECK_ID);
    expect(binding, `index.ts never selects #${RECHECK_ID} — the button is decoration`).not.toBe('');
    const handler = clickHandler(binding);
    expect(handler, `no click handler registered for #${RECHECK_ID}`).not.toBe('');
    // recheckAuthStatus() is the existing path: it reads status *and* applies it through
    // applyAuthStatus/renderAuthScreens. A handler that calls render() alone, or that re-reads
    // status without applying it, is a button that visibly does nothing — the exact thing worth
    // less than no button at all.
    expect(handler, `#${RECHECK_ID}'s handler does not re-read auth status`).toMatch(/recheckAuthStatus\(\)|window\.voidTerminal\.auth\.status\(\)/);
    expect(handler, `#${RECHECK_ID}'s handler starts a login — the action that succeeds and returns the person to this same screen`).not.toMatch(/startSignIn\(|loginStart\(|beginLogin\(/);
    expect(renderer.match(new RegExp(`${binding}\\.addEventListener\\('click',`, 'g'))?.length, `#${RECHECK_ID}'s click handler is missing or registered more than once`).toBe(1);
  });
});

describe('the copy names the situation in the vocabulary that already exists, and invents nothing', () => {
  it('uses the words the state itself is named with: access, and not granted yet', () => {
    expect(sectionText, `index.html has no #${SECTION_ID} copy`).not.toBe('');
    expect(sectionText, `#${SECTION_ID} never says what is missing: "${sectionText}"`).toMatch(/\baccess\b/i);
    expect(sectionText, `#${SECTION_ID} never says access has not been granted: "${sectionText}"`).toMatch(/\bgrant(ed)?\b/i);
  });

  it('names nothing the product has not decided — no 402, no subscription, no budget, no money', () => {
    // What "granted access" is made of on the server is an open product question (a subscription
    // row, an operator grant, a trial — see internal/auth/errors.go, which deliberately names
    // none of them). Relay's own wire name for this refusal, budget_exceeded, is the cautionary
    // example: it points at a monthly budget the case has nothing to do with. Copy that guesses
    // has to be rewritten the day the question is answered, and until then it is simply wrong.
    expect(sectionText, `index.html has no #${SECTION_ID} copy to check`).not.toBe('');
    const banned: [RegExp, string][] = [
      [/\b402\b/, 'a status code, which is not a sentence a person can act on'],
      [/\bsubscri\w*/i, 'a subscription — the server has one row for 72 subjects; this is not what happened'],
      [/\bbudget\b/i, "Relay's misleading wire name for this refusal, about a monthly budget this has nothing to do with"],
      [/\b(pay|paid|payment|billing|invoice|purchase|buy)\b/i, 'money, which nobody has decided is part of this'],
      [/\b(upgrade|plan|tier|trial|quota|credits?)\b/i, 'a product shape that does not exist yet'],
      [/[$€₽]/, 'a currency symbol'],
    ];
    for (const [pattern, why] of banned) {
      const hit = sectionText.match(pattern);
      expect(hit, `#${SECTION_ID} says "${hit?.[0]}" — ${why}`).toBeNull();
    }
  });

  it('does not tell the person their sign-in failed, expired or never happened — it worked', () => {
    // Everything in this list is false for them, and each is one careless sentence away: the
    // nearest existing copy ("Your sign-in has expired or was revoked") is exactly it.
    expect(sectionText, `index.html has no #${SECTION_ID} copy to check`).not.toBe('');
    for (const pattern of [/\bexpired\b/i, /\brevoked\b/i, /\bnever signed in\b/i, /\bnot signed in\b/i, /\bsigned out\b/i, /\bfailed\b/i, /\btry again\b/i]) {
      const hit = sectionText.match(pattern);
      expect(hit, `#${SECTION_ID} says "${hit?.[0]}", which is not true of someone whose sign-in just succeeded: "${sectionText}"`).toBeNull();
    }
  });

  it('shows no identity — there is none to show, and the one the server echoed is not one', () => {
    // The refusal lands before identity, and vc does not read the body of it: a subject supplied
    // by the service that just said no is not an identity anyone vouched for. So there is no
    // email to put here, and a screen that shows one is showing something it made up.
    const binding = bindingFor(SECTION_ID);
    expect(binding, `index.ts never selects #${SECTION_ID}`).not.toBe('');
    expect(renderer, `index.ts writes text into ${binding} at runtime — the only sources available are vc's raw refusal sentence and an unverified subject, and neither belongs on screen`)
      .not.toMatch(new RegExp(`${binding}\\.(?:textContent|innerHTML)\\s*=`));
    expect(sectionText, `#${SECTION_ID} carries an email address in the markup`).not.toMatch(/@/);
  });

  it('does not send the person looking for a human — the same rule the rest of the sign-in copy already lives under', () => {
    // tests/no-operator-copy.test.ts scans index.html as a whole and already fails on this; the
    // check is repeated here because this screen is where the temptation is strongest. vc's own
    // sentence ends "— an operator has to grant it", and pasting it in is the shortest route from
    // errors.go to the window. This product has nobody on the other end for the person to find:
    // naming a role they cannot reach turns a wait into a search that ends nowhere.
    expect(sectionText, `index.html has no #${SECTION_ID} copy to check`).not.toBe('');
    for (const pattern of [/\boperator\b/i, /\badmin(istrator)?\b/i, /\bsupport (team|desk|staff)\b/i, /\b(ask|contact|email|reach out to)\s+(someone|us|your|the)\b/i]) {
      const hit = sectionText.match(pattern);
      expect(hit, `#${SECTION_ID} says "${hit?.[0]}" — it hands the person's problem to someone they have no way to reach`).toBeNull();
    }
  });
});

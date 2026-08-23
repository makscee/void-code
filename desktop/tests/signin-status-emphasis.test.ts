import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Same approach as tests/login-progress-wiring.test.ts and tests/signin-code-screen.test.ts:
// index.ts has DOM side effects on import and this suite has no jsdom/happy-dom environment
// (package.json pulls neither), so the wiring is pinned as text against the source that draws it.
//
// What this file is about: index.css already carries `#signin-status.error{color:#f9534c}` — the
// one rule that makes a failed sign-in look different from the ordinary grey progress line — and
// nothing in the renderer ever puts that class on the element. The rule is dead code today, so
// the sentence describeLoginFailure produces is drawn in the same #848b95 grey as everything
// else on the panel. What has to become true: #signin-status carries the class `error` while the
// login phase is 'error', and does not carry it in any other phase.
//
// Honest limit: this is a text scan. It proves renderAuthScreens mutates the class and that the
// mutation is a function of loginPhase; it cannot prove the class actually lands on the element
// at runtime, or that the resulting colour is legible against #171b20 — those need a real
// DOM/Electron run, which nothing in this suite has.

const html = readFileSync(new URL('../src/renderer/index.html', import.meta.url), 'utf8');
const css = readFileSync(new URL('../src/renderer/index.css', import.meta.url), 'utf8');
const renderer = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');

// Same extraction as tests/login-progress-wiring.test.ts, deliberately: the class has to be set
// where the text is set, on every render, and not once at startup or from inside a click handler
// that only fires on one of the several paths into the error phase (a push can land on 'error'
// with no click involved at all — see reduceLoginPush on a closed/ok:false event).
const renderFn = renderer.match(/function renderAuthScreens\(\): void \{[\s\S]{0,1500}?\n\}/)?.[0] ?? '';

describe('the rule this hangs on actually exists and applies to this element', () => {
  it('index.css still styles #signin-status.error, and still differently from the plain line', () => {
    expect(css, 'index.css no longer has a #signin-status.error rule to switch on').toMatch(/#signin-status\.error\s*\{[^}]*color:/);
    const plain = css.match(/#signin-status\{[^}]*\}/)?.[0] ?? '';
    const failing = css.match(/#signin-status\.error\{[^}]*\}/)?.[0] ?? '';
    expect(plain, 'could not locate the base #signin-status rule').not.toBe('');
    const plainColour = plain.match(/color:(#[0-9a-f]{3,8})/i)?.[1];
    const failingColour = failing.match(/color:(#[0-9a-f]{3,8})/i)?.[1];
    expect(failingColour, 'the error rule sets no colour of its own').toBeTruthy();
    expect(failingColour, 'the error state is painted the same colour as the ordinary status line').not.toBe(plainColour);
  });

  it('the element the rule targets is the same one loginStatusText writes into', () => {
    expect(html).toMatch(/id="signin-status"/);
    expect(renderer).toMatch(/const signinStatusElement = document\.querySelector<HTMLElement>\('#signin-status'\)/);
    expect(renderFn, 'could not locate renderAuthScreens()').not.toBe('');
    expect(renderFn).toMatch(/signinStatusElement\.textContent\s*=\s*loginStatusText\(/);
  });

  it('index.html does not ship the class pre-set in the markup', () => {
    // A "fix" that puts class="error" in index.html would make every status line red — including
    // the ones this same change is removing, and including the empty one that shows on idle.
    const statusTag = html.match(/<[a-z]+[^>]*id="signin-status"[^>]*>/)?.[0] ?? '';
    expect(statusTag, 'could not locate the #signin-status element in index.html').not.toBe('');
    expect(statusTag).not.toMatch(/class=/);
  });
});

describe('renderAuthScreens puts the class on for the error phase and takes it off for every other one', () => {
  it('mutates the status element\'s class list at all', () => {
    expect(renderFn, 'could not locate renderAuthScreens()').not.toBe('');
    expect(renderFn, 'renderAuthScreens() never touches signinStatusElement.classList — the #signin-status.error rule stays dead and a failed sign-in reads as ordinary grey copy')
      .toMatch(/signinStatusElement\.classList\./);
  });

  it('uses the exact class name the stylesheet selects on', () => {
    const mutations = renderFn.match(/signinStatusElement\.classList\.\w+\(\s*'([^']*)'/g) ?? [];
    expect(mutations.length, 'no classList call on signinStatusElement carries a class name literal').toBeGreaterThan(0);
    for (const mutation of mutations) {
      expect(mutation, `classList is mutated with a class the stylesheet does not select on: ${mutation}`).toMatch(/'error'/);
    }
  });

  it('removes the class again as well as adding it — a one-way .add() leaves the line red forever', () => {
    // The lazy pass: `signinStatusElement.classList.add('error')` guarded by an if, and nothing
    // in the else. The first failed attempt turns the line red; the retry that then succeeds, and
    // every empty status line after it, stays red. Either form closes it — a toggle with an
    // explicit second argument, or an add paired with a remove.
    const toggled = /signinStatusElement\.classList\.toggle\(\s*'error'\s*,/.test(renderFn);
    const addedAndRemoved = /signinStatusElement\.classList\.add\(\s*'error'/.test(renderFn)
      && /signinStatusElement\.classList\.remove\(\s*'error'/.test(renderFn);
    expect(toggled || addedAndRemoved,
      'the error class is only ever put on, never taken off: use classList.toggle(\'error\', <condition>) or pair add with remove').toBe(true);
  });

  it('drives the class off the login phase, not off the status text being non-empty', () => {
    // `loginStatusText(loginPhase) !== ''` is the tempting shortcut and it is wrong for the same
    // reason twice over: it is true today for 'starting' and 'authorized' (which are being made
    // silent in this same change, so the shortcut would paint the in-progress line red until
    // then), and it silently re-couples the colour to the copy, so any future non-error sentence
    // on this line arrives red. The colour is a statement about the phase.
    const decision = renderFn.match(/signinStatusElement\.classList\.\w+\([^)]*\)/g)?.join('\n') ?? '';
    expect(decision, 'no classList mutation found to inspect').not.toBe('');
    expect(decision, 'the error class is decided from the rendered text rather than from the phase')
      .not.toMatch(/loginStatusText/);
    expect(decision, 'the error class is not derived from loginPhase — it must be a function of the phase, whether inline or through a helper called with it')
      .toMatch(/loginPhase/);
  });

  it('does not bypass the stylesheet by writing the colour inline', () => {
    // An inline style.color would work on screen and defeat the point: the colour would live in
    // two places, and the CSP-governed stylesheet would no longer be the thing that decides it.
    expect(renderFn).not.toMatch(/signinStatusElement\.style\./);
    expect(renderFn).not.toMatch(/#f9534c/i);
  });
});

import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Same approach as tests/renderer-login.test.ts: index.ts has DOM side effects on import and
// there is no jsdom environment configured here, so the wiring is pinned as text against the
// pure, already-unit-tested decisions in auth-view.ts (see tests/login-progress.test.ts). What
// is pinned here is that index.ts and index.html actually surface those decisions — a working
// button that says "Signing in…", a status line that is actually read and actually rendered
// somewhere in the markup, and that the button is disabled while starting the same way it is
// already disabled while a code is on screen.

const html = readFileSync(new URL('../src/renderer/index.html', import.meta.url), 'utf8');
const renderer = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');

describe('the sign-in button shows that a click did something', () => {
  it('imports signInButtonLabel and beginLogin from auth-view alongside the existing decisions', () => {
    expect(renderer).toMatch(/from ['"]\.\/auth-view['"]/);
    for (const name of ['signInButtonLabel', 'beginLogin', 'loginStatusText']) {
      expect(renderer, `index.ts does not reference ${name}`).toContain(name);
    }
  });

  it('drives the button\'s visible text from signInButtonLabel, not a hand-rolled ternary that only knows about invalid_credential', () => {
    // The pre-existing ternary (authScreen === 'invalid_credential' ? 'Sign in again' : 'Sign
    // in') has no branch for "a login is currently starting" — that is exactly the gap
    // signInButtonLabel was built to close. Requiring the call site, rather than banning the
    // old literal text, tolerates signInButtonLabel itself still producing those same strings.
    expect(renderer).toMatch(/signinStartButton\.textContent\s*=\s*signInButtonLabel\(/);
  });

  it('still guards the click handler with canStartLogin (unchanged contract) and now also drives the transition through beginLogin', () => {
    const handlerBlock = renderer.match(/signinStartButton\.addEventListener\('click',[\s\S]{0,400}?\}\);/)?.[0] ?? '';
    expect(handlerBlock, 'no addEventListener("click", ...) block found on the sign-in button').not.toBe('');
    expect(handlerBlock).toMatch(/canStartLogin\(/);
    expect(handlerBlock, 'click handler does not call beginLogin — the button has nothing to show between the click and the first push from main').toMatch(/beginLogin\(/);
    // Exactly one registration, same guard as the pre-existing test in renderer-login.test.ts —
    // repeated here because this handler block is exactly what a lazy fix to this feature would
    // touch, and a duplicate registration would silently double-fire beginLogin per click.
    expect(renderer.match(/signinStartButton\.addEventListener\('click',/g)?.length).toBe(1);
  });

  it('re-renders the auth screens after beginLogin changes the phase — a click that only updates the in-memory phase without calling renderAuthScreens() never reaches the screen', () => {
    const handlerBlock = renderer.match(/signinStartButton\.addEventListener\('click',[\s\S]{0,400}?\}\);/)?.[0] ?? '';
    const beginLoginIndex = handlerBlock.indexOf('beginLogin(');
    const renderIndex = handlerBlock.indexOf('renderAuthScreens(');
    expect(beginLoginIndex, 'click handler does not call beginLogin').toBeGreaterThan(-1);
    expect(renderIndex, 'click handler does not call renderAuthScreens after beginLogin').toBeGreaterThan(-1);
    expect(renderIndex).toBeGreaterThan(beginLoginIndex);
  });

  it('disabled state is still driven by canStartLogin, which a starting phase must also fail (pinned directly in tests/login-progress.test.ts)', () => {
    expect(renderer).toMatch(/signinStartButton\.disabled\s*=\s*!canStartLogin\(/);
  });
});

describe('a status line exists and is actually wired to loginStatusText', () => {
  it('index.html has a status element for the login phase, not folded into #signin-code-status which only exists while a code is showing', () => {
    // #signin-code-status only renders while showingCode is true (see renderAuthScreens); it has
    // no way to say anything during 'starting' or 'error', which is exactly the gap being closed.
    expect(html, 'index.html has no #signin-status element').toMatch(/id="signin-status"/);
  });

  it('the new status element sits inside the sign-in section, not some unrelated part of the page', () => {
    const preflightSection = html.match(/<section id="preflight"[\s\S]*?<\/section>/)?.[0] ?? '';
    expect(preflightSection, 'could not locate the #preflight section').not.toBe('');
    expect(preflightSection).toMatch(/id="signin-status"/);
  });

  it('index.ts selects the element and assigns its text from loginStatusText inside renderAuthScreens', () => {
    expect(renderer).toMatch(/querySelector<HTMLElement>\('#signin-status'\)/);
    const renderFn = renderer.match(/function renderAuthScreens\(\): void \{[\s\S]{0,1500}?\n\}/)?.[0] ?? '';
    expect(renderFn, 'could not locate renderAuthScreens()').not.toBe('');
    expect(renderFn, 'renderAuthScreens() does not assign the status element\'s text from loginStatusText(loginPhase)').toMatch(/\.textContent\s*=\s*loginStatusText\(loginPhase\)/);
  });
});

describe('the failure sentence itself never violates the operator/terminal copy guard', () => {
  // tests/no-operator-copy.test.ts scans RECOVERY_GUIDANCE, index.ts and index.html, but the new
  // failure copy lives in describeLoginFailure (auth-view.ts) and is generated at runtime — none
  // of the three existing scans would ever see it. Covered directly in
  // tests/no-operator-copy.test.ts itself (new case added there, see that file), not duplicated
  // here; this file only asserts that the copy is reached through loginStatusText, which the
  // guard test exercises.
  it('renderAuthScreens does not compose its own error text inline instead of calling loginStatusText', () => {
    const renderFn = renderer.match(/function renderAuthScreens\(\): void \{[\s\S]{0,1500}?\n\}/)?.[0] ?? '';
    expect(renderFn).not.toMatch(/phase\.reason/);
  });
});

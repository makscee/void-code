import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// index.ts side-effects on import (top-level `document.querySelector(...)!`, then kicks off
// `workspace.load()` against a real IPC bridge) and there is no jsdom/happy-dom environment
// configured for this project — see recovery-ux.test.ts and renderer-packaging.test.ts, which
// already test this exact file the same way, as text. The behavioural decisions this feature
// depends on (which screen, when a login may start, code freshness) live instead in the pure,
// importable src/renderer/auth-view.ts and are exercised for real in auth-view.test.ts. What is
// pinned here is the wiring: that index.ts and index.html actually use that module and actually
// replace the old "find an operator" instructions, rather than leaving them in place beside a
// button that does nothing.

const html = readFileSync(new URL('../src/renderer/index.html', import.meta.url), 'utf8');
const css = readFileSync(new URL('../src/renderer/index.css', import.meta.url), 'utf8');
const renderer = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');
const preload = readFileSync(new URL('../src/preload/index.ts', import.meta.url), 'utf8');
const globalTypes = readFileSync(new URL('../src/renderer/global.d.ts', import.meta.url), 'utf8');
const contract = readFileSync(new URL('../src/shared/contract.ts', import.meta.url), 'utf8');
const smoke = readFileSync(new URL('../src/renderer/smoke.ts', import.meta.url), 'utf8');
const packagedSmokeCheck = readFileSync(new URL('../scripts/packaged-smoke.mjs', import.meta.url), 'utf8');

describe('the terminal wall is actually removed, not just relabelled', () => {
  it('no longer tells a signed-out person to find an operator or a terminal outside the app', () => {
    expect(html).not.toContain('Ask your operator to confirm that existing VC sign-in');
    expect(html).not.toContain('guided VC login outside this app');
    // The support report disclosure legitimately still says "terminal output" (what it excludes) —
    // this only pins that the *sign-in instruction* paragraph is gone, not a blanket word ban.
  });

  it('stops asserting a chat works "using your existing VC authentication" before anyone has signed in', () => {
    expect(html).not.toContain('using your existing VC authentication');
  });

  it('gives the signed-out person a real action: a same-app sign-in button', () => {
    expect(html).toMatch(/<button id="signin-start" type="button"[^>]*>/);
  });
});

describe('the three auth screens are structurally distinct, not one div with swapped text', () => {
  it('markup carries separate containers for signed_out, invalid_credential and the device code', () => {
    // Separate ids (rather than one element whose textContent gets overwritten) is what lets
    // renderer-authority-style checks, and a person glancing at the DOM, tell the states apart —
    // and what stops a lazy implementation from serving stale copy from the wrong branch.
    for (const id of ['signin-signed-out', 'signin-invalid', 'signin-code']) {
      expect(html, `index.html is missing #${id}`).toMatch(new RegExp(`id="${id}"`));
    }
    expect(html).toMatch(/id="signin-code-value"/);
  });

  it('the invalid-credential copy does not reuse the "never signed in" copy', () => {
    const invalidSection = html.match(/id="signin-invalid"[^>]*>([\s\S]*?)<\/(?:div|section)>/)?.[1] ?? '';
    expect(invalidSection.length, 'could not locate #signin-invalid content to check its copy').toBeGreaterThan(0);
    expect(invalidSection).not.toMatch(/never signed in/i);
  });
});

describe('the code stays on screen and stays readable', () => {
  it('the code value is not put through the ellipsis/truncation treatment used for paths and titles', () => {
    // #recovery-path and .recent-row span both rely on overflow:hidden + text-overflow:ellipsis
    // for long values — fine for a folder path, fatal for a code someone has to copy correctly.
    const rule = css.match(/#signin-code-value\{[^}]*\}/)?.[0] ?? '';
    expect(rule, 'index.css has no #signin-code-value rule').not.toBe('');
    expect(rule).not.toContain('text-overflow:ellipsis');
  });

  it('nothing in the renderer clears or hides the code on window blur/visibility change', () => {
    expect(renderer).not.toMatch(/addEventListener\('(blur|visibilitychange)'[\s\S]{0,200}signin/);
  });
});

describe('index.ts is actually wired to the pure auth-view state machine, not reimplementing it inline', () => {
  it('imports the decision functions from auth-view rather than branching on authState/event strings directly', () => {
    expect(renderer).toMatch(/from ['"]\.\/auth-view['"]/);
    for (const name of ['screenForStatus', 'reduceLoginPush', 'canStartLogin', 'requiresStatusRecheck']) {
      expect(renderer, `index.ts does not reference ${name}`).toContain(name);
    }
  });

  it('guards the click handler with canStartLogin so a second click cannot start a second login', () => {
    const handlerBlock = renderer.match(/signinStartButton\.addEventListener\('click',[\s\S]{0,400}?\}\);/)?.[0] ?? '';
    expect(handlerBlock, 'no addEventListener("click", ...) block found on the sign-in button').not.toBe('');
    expect(handlerBlock).toMatch(/canStartLogin\(/);
    // Exactly one registration — a lazy fix could pass the guard test above while still wiring the
    // listener twice (e.g. once in render(), once at module scope), which reintroduces the double-start.
    expect(renderer.match(/signinStartButton\.addEventListener\('click',/g)?.length).toBe(1);
  });

  it('opens the verification URL through the existing guarded IPC, not window.open or a URL it composed itself', () => {
    expect(renderer).not.toContain('window.open(');
    // openLink is the same channel .../shared/contract.ts already restricts to http(s) — reusing it
    // means the sign-in URL gets that validation for free instead of a fresh, unaudited path.
    expect(renderer).toMatch(/window\.voidTerminal\.(?:auth\.)?openLink\(\s*(?:event\.)?verificationUrl/);
  });

  it('re-reads auth status after a login reaches a phase that requires it, instead of trusting the push alone', () => {
    expect(renderer).toMatch(/requiresStatusRecheck\(/);
    // status() must be called at least twice in the source: once to establish the initial screen,
    // once more somewhere conditioned on requiresStatusRecheck. A single call site can only be the
    // initial load, which would leave the screen exactly where it was before the login resolved.
    const statusCalls = renderer.match(/\.auth\.status\(\)/g)?.length ?? 0;
    expect(statusCalls, 'window.voidTerminal.auth.status() is called fewer than twice in index.ts').toBeGreaterThanOrEqual(2);
  });
});

describe('a chat start failure re-checks auth before choosing the generic failure or a sign-in screen', () => {
  // launch()'s single catch block (there is exactly one `catch (error)` in this file — see the
  // "officialXterm"-style greps above) is where SESSION_START_FAILED / SESSION_MISSING already
  // get decided. That is also where the auth re-check and routing decision have to live.
  const catchBlock = renderer.match(/catch \(error\) \{[\s\S]{0,800}?\n {2}\}\n\}/)?.[0] ?? '';

  it('locates the single catch block in launch()', () => {
    expect(catchBlock, 'could not find the catch (error) block in launch()').not.toBe('');
  });

  it('imports and uses routeStartFailure from auth-view rather than re-deriving the decision inline', () => {
    expect(renderer).toMatch(/from ['"]\.\/auth-view['"]/);
    expect(renderer).toContain('routeStartFailure');
    expect(catchBlock).toMatch(/routeStartFailure\(/);
  });

  it('re-reads auth status inside the catch block itself — not the authScreen value captured before the failed start', () => {
    // recheckAuthStatus() and window.voidTerminal.auth.status() are the only two things in this
    // file that actually ask vc again; reading the outer `authScreen` variable without an await
    // here would act on whatever was true at app start (or after the last unrelated event), which
    // is exactly the "cached status" defect this routing exists to avoid.
    const freshReadMatch = catchBlock.match(/await\s+(?:recheckAuthStatus\(\)|window\.voidTerminal\.auth\.status\(\))/);
    expect(freshReadMatch, 'catch block does not await a fresh auth status read').not.toBeNull();
    const routeCallIndex = catchBlock.indexOf('routeStartFailure(');
    expect(routeCallIndex, 'catch block does not call routeStartFailure').toBeGreaterThan(-1);
    // The fresh read must happen before the routing decision uses it, not after.
    expect(freshReadMatch!.index!).toBeLessThan(routeCallIndex);
  });

  it('only shows the generic failure screen for the generic route — a start failure while not signed in must not also flash "chat could not start"', () => {
    // routeStartFailure's own contract (pinned in auth-view.test.ts) returns screen: 'generic' vs
    // screen: 'signin'. A lazy fix could call routeStartFailure only to ignore its result and call
    // showEnded unconditionally either way; requiring the literal discriminant here closes that.
    expect(catchBlock).toMatch(/\.screen\s*===\s*['"]generic['"]/);
    // showEnded must appear inside that conditional, not before/regardless of it.
    const conditionIndex = catchBlock.search(/\.screen\s*===\s*['"]generic['"]/);
    const showEndedIndex = catchBlock.indexOf('showEnded(');
    expect(showEndedIndex, 'catch block does not call showEnded at all').toBeGreaterThan(-1);
    expect(showEndedIndex).toBeGreaterThan(conditionIndex);
  });
});

describe('preload surface: auth is merged into the one guarded, smoke-checked global', () => {
  // The packaged smoke check (src/renderer/smoke.ts) only ever reads Object.keys(window.voidTerminal).
  // A second top-level global is invisible to it — a guard that cannot see half the exposed surface
  // is not a guard. Merging auth under voidTerminal.auth keeps it inside the one surface that is
  // actually asserted against a fixed allowlist at packaging time.
  it('does not expose a second, unchecked top-level global for auth', () => {
    expect(preload).not.toMatch(/exposeInMainWorld\(\s*['"]voidAuth['"]/);
  });

  it('exposes auth as a frozen sub-object of the existing voidTerminal api', () => {
    expect(preload).toMatch(/auth:\s*Object\.freeze\(\{[\s\S]{0,300}?loginStart[\s\S]{0,300}?\}\)/);
  });

  it('the shared TerminalApi type and the renderer global declaration both know about auth', () => {
    expect(contract).toMatch(/auth:\s*\{[\s\S]{0,300}?loginStart[\s\S]{0,300}?onLoginEvent/);
    expect(globalTypes).not.toMatch(/voidAuth:/);
  });

  it('the packaged smoke check\'s api allowlist is updated for the merged surface, not silently invalidated', () => {
    // If this file still lists the pre-login-button api and index.ts starts exposing `auth`, the
    // packaged smoke check fails on every build — the guard that was supposed to catch a missing key
    // instead breaks the pipeline on an expected addition. That trade only works if this line is updated.
    expect(smoke).toContain('Object.keys(window.voidTerminal)');
    expect(packagedSmokeCheck).toMatch(/expectedApi\s*=\s*\[[^\]]*'auth'[^\]]*\]/);
  });
});

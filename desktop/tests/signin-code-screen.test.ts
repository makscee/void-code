import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Same approach as tests/renderer-login.test.ts and tests/login-progress-wiring.test.ts: index.ts
// has DOM side effects on import and there is no jsdom/happy-dom environment configured here, so
// the redesigned code screen is pinned as text against the markup and the source that wires it.
// What this file establishes: the three steps exist as real structure (not one paragraph with
// numbers typed in), the link and the code are each independently actionable, copy goes through
// the existing IPC clipboard path from two trigger points, and the countdown and the "a browser
// opened" claim are the ones actually fixed, not just relabelled.
//
// Honest limit: this is a text scan. It proves the source *calls* the right functions and *wires*
// the right elements to the right handlers; it cannot prove a click actually reaches the screen, that
// the clipboard actually receives the text, or that the three steps are laid out as three steps
// visually — those require a real DOM/Electron run, which nothing in this test suite has.

const html = readFileSync(new URL('../src/renderer/index.html', import.meta.url), 'utf8');
const renderer = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');
const preload = readFileSync(new URL('../src/preload/index.ts', import.meta.url), 'utf8');
const mainSource = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');
const preloadContract = readFileSync(new URL('../src/shared/preload-contract.ts', import.meta.url), 'utf8');
const contract = readFileSync(new URL('../src/shared/contract.ts', import.meta.url), 'utf8');

// The exact markup region this file cares about — everything the redesigned #signin-code screen
// owns. Falls back to '' so every assertion below fails loudly (not with a cryptic regex miss)
// if the container itself is missing or was renamed.
const codeSection = html.match(/<div id="signin-code"[^>]*>[\s\S]*?<\/div>\s*(?=<div id="signin-ready")/)?.[0] ?? '';

describe('the three steps exist as real structure, not one paragraph with numbers typed in', () => {
  it('locates the #signin-code container at all', () => {
    expect(codeSection, 'could not locate the #signin-code screen in index.html').not.toBe('');
  });

  it('carries an ordered list with exactly three steps, each independently addressable by id', () => {
    // Exactly three <li> — not "at least three" — so a later change collapsing two steps back
    // into one (or splitting one into two silently) both fail this the same way.
    expect(codeSection).toMatch(/<ol id="signin-steps">/);
    const items = codeSection.match(/<li\b[^>]*>/g) ?? [];
    expect(items.length, `expected exactly 3 <li> steps, found ${items.length}`).toBe(3);
    for (const id of ['signin-step-open', 'signin-step-code', 'signin-step-wait']) {
      expect(codeSection, `#signin-code is missing step container #${id}`).toMatch(new RegExp(`id="${id}"`));
    }
  });

  it('keeps the three steps in the order the owner specified: open the page, enter the code, come back', () => {
    const openIndex = codeSection.indexOf('id="signin-step-open"');
    const codeIndex = codeSection.indexOf('id="signin-step-code"');
    const waitIndex = codeSection.indexOf('id="signin-step-wait"');
    expect(openIndex).toBeGreaterThan(-1);
    expect(codeIndex).toBeGreaterThan(openIndex);
    expect(waitIndex).toBeGreaterThan(codeIndex);
  });
});

describe('the link is present, readable, and can be triggered again — not opened once and lost', () => {
  it('renders the verification URL as its own readable element inside step 1, distinct from the Open button', () => {
    const step = codeSection.match(/<li id="signin-step-open"[\s\S]*?<\/li>/)?.[0] ?? '';
    expect(step, 'could not locate #signin-step-open').not.toBe('');
    expect(step, 'step 1 has no #signin-link element to display the URL as text').toMatch(/id="signin-link"/);
    // The link text and the retry action are deliberately two elements, not one clickable link:
    // a real <a href> inside a sandboxed, contextIsolation renderer would attempt in-app
    // navigation instead of going through the already-guarded openLink IPC (the exact path
    // src/main/index.ts already restricts to http(s) via linkRequest). Requiring #signin-link to
    // NOT itself be the button closes off "wire the whole line as an <a>" as a shortcut.
    const linkTag = step.match(/<[a-z]+[^>]*id="signin-link"[^>]*>/)?.[0] ?? '';
    expect(linkTag.toLowerCase().startsWith('<a')).toBe(false);
    expect(linkTag.toLowerCase().startsWith('<button')).toBe(false);
  });

  it('gives step 1 its own Open button, distinct from #signin-link, so the link can be triggered again if the browser did not come forward the first time', () => {
    const step = codeSection.match(/<li id="signin-step-open"[\s\S]*?<\/li>/)?.[0] ?? '';
    const buttonTag = step.match(/<button[^>]*id="signin-link-open"[^>]*>/)?.[0] ?? '';
    expect(buttonTag, 'step 1 has no #signin-link-open button').not.toBe('');
    expect(buttonTag).toMatch(/type="button"/);
  });

  it('index.ts fills #signin-link from the current code prompt, not a value frozen from the original push event', () => {
    expect(renderer).toMatch(/querySelector<HTMLElement>\('#signin-link'\)/);
    // renderAuthScreens already re-runs on every tick (see the code timer) and is the one place
    // reading loginPhase while phase === 'code' is guaranteed current. Sourcing #signin-link's
    // text from event.verificationUrl captured once in handleLoginPush, instead of from
    // loginPhase here, would go stale the moment a second login replaced the phase.
    const renderFn = renderer.match(/function renderAuthScreens\(\): void \{[\s\S]{0,2000}?\n\}/)?.[0] ?? '';
    expect(renderFn, 'could not locate renderAuthScreens()').not.toBe('');
    expect(renderFn, 'renderAuthScreens() does not set #signin-link from loginPhase.verificationUrl').toMatch(/signinLinkElement\.textContent\s*=\s*loginPhase\.verificationUrl/);
  });

  it('the Open button re-invokes the guarded openLink IPC against the current prompt\'s URL, not window.open or a stored copy of the original event', () => {
    const handlerBlock = renderer.match(/signinLinkOpenButton\.addEventListener\('click',[\s\S]{0,400}?\}\);/)?.[0] ?? '';
    expect(handlerBlock, 'no click handler found for #signin-link-open').not.toBe('');
    expect(handlerBlock).not.toContain('window.open(');
    expect(handlerBlock, 'the Open button does not call openLink against loginPhase.verificationUrl').toMatch(/window\.voidTerminal\.openLink\(\s*loginPhase\.verificationUrl/);
    expect(renderer.match(/signinLinkOpenButton\.addEventListener\('click',/g)?.length, 'the Open button handler is registered more than once').toBe(1);
  });
});

describe('the code screen no longer claims a browser opened', () => {
  it('drops the old sentence that stated it as fact', () => {
    expect(html).not.toContain('Open the link that just opened');
  });

  it('nothing in the redesigned step 1 or step 2 copy asserts a browser already opened — a claim that stays false whenever openLink silently did nothing', () => {
    const step1 = codeSection.match(/<li id="signin-step-open"[\s\S]*?<\/li>/)?.[0] ?? '';
    const step2 = codeSection.match(/<li id="signin-step-code"[\s\S]*?<\/li>/)?.[0] ?? '';
    for (const [name, step] of [['step 1', step1], ['step 2', step2]] as const) {
      expect(step, `${name}: "just opened"`).not.toMatch(/just opened/i);
      expect(step, `${name}: "already opened"`).not.toMatch(/already opened/i);
      expect(step, `${name}: "the link opened"`).not.toMatch(/the link (?:has )?opened/i);
    }
  });
});

describe('the code can be copied — by the Copy button and by clicking the code itself', () => {
  it('step 2 carries the existing #signin-code-value plus a dedicated Copy button', () => {
    const step = codeSection.match(/<li id="signin-step-code"[\s\S]*?<\/li>/)?.[0] ?? '';
    expect(step, 'could not locate #signin-step-code').not.toBe('');
    expect(step).toMatch(/id="signin-code-value"/);
    const copyButton = step.match(/<button[^>]*id="signin-code-copy"[^>]*>/)?.[0] ?? '';
    expect(copyButton, 'step 2 has no #signin-code-copy button').not.toBe('');
    expect(copyButton).toMatch(/type="button"/);
  });

  it('#signin-code-value is marked as its own actionable control (role+tabindex), not a plain span that only happens to have a click listener nobody can reach by keyboard', () => {
    const valueTag = codeSection.match(/<[a-z]+[^>]*id="signin-code-value"[^>]*>/)?.[0] ?? '';
    expect(valueTag, 'could not locate the #signin-code-value tag').not.toBe('');
    expect(valueTag).toMatch(/role="button"/);
    expect(valueTag).toMatch(/tabindex="0"/);
  });

  it('both the Copy button and the code value itself are wired to the same copy call, exactly once each', () => {
    for (const [name, pattern] of [
      ['#signin-code-copy', /signinCodeCopyButton\.addEventListener\('click',/g],
      ['#signin-code-value', /signinCodeValueElement\.addEventListener\('click',/g],
    ] as const) {
      const matches = renderer.match(pattern);
      expect(matches?.length, `${name} click handler is missing or registered more than once`).toBe(1);
    }
  });

  it('both handlers call the clipboard IPC (not a renderer-only navigator.clipboard call, which is not dependable under contextIsolation) with the current code', () => {
    for (const name of ['signinCodeCopyButton', 'signinCodeValueElement']) {
      const handlerBlock = renderer.match(new RegExp(`${name}\\.addEventListener\\('click',[\\s\\S]{0,400}?\\}\\);`))?.[0] ?? '';
      expect(handlerBlock, `no click handler block found for ${name}`).not.toBe('');
      expect(handlerBlock, `${name}'s handler does not call window.voidTerminal.auth.copyCode`).toMatch(/window\.voidTerminal\.auth\.copyCode\(/);
      // Must copy the code currently on screen, not a value captured once when the prompt first
      // arrived — same staleness trap as the Open button above.
      expect(handlerBlock, `${name}'s handler does not pass loginPhase.userCode to copyCode`).toMatch(/copyCode\(\s*loginPhase\.userCode/);
    }
    expect(renderer).not.toMatch(/navigator\.clipboard/);
  });

  // Changed from the original behaviour, which routed the copy confirmation through announce()
  // alone — a full-width bar painted under the header, at the top of the window. The owner's
  // point 3: the person's eye is on the code in the middle of the screen, about to leave for the
  // browser, and the confirmation has to be where the action was, not somewhere they have to look
  // away from the code to see. "Near the code" is pinned here as a source-level fact: a dedicated
  // status element that lives inside step 2's own <li> (the same list item as #signin-code-value
  // and the Copy button), not the top-of-window #notice element announce() writes to. A lazy
  // implementation could satisfy "confirmation exists somewhere" by writing into
  // #signin-code-status in step 3 (already on screen, already wired to the countdown) — that is
  // rejected below by scoping the search to step 2's own <li>, not the whole code section.
  describe('the copy confirmation reaches the person near the code, not only in the top banner', () => {
    it('step 2 carries its own confirmation element, distinct from #signin-code-status (step 3) and #notice (top banner)', () => {
      const step2 = codeSection.match(/<li id="signin-step-code"[\s\S]*?<\/li>/)?.[0] ?? '';
      expect(step2, 'could not locate #signin-step-code').not.toBe('');
      expect(step2, 'step 2 has no #signin-code-copy-status element').toMatch(/id="signin-code-copy-status"/);
      // Must not be the same element as step 3's countdown status — that would mean the
      // confirmation is still landing away from the code, just renamed.
      const step3 = codeSection.match(/<li id="signin-step-wait"[\s\S]*?<\/li>/)?.[0] ?? '';
      expect(step3, 'could not locate #signin-step-wait').not.toBe('');
      expect(step3).not.toMatch(/id="signin-code-copy-status"/);
    });

    it('index.ts selects #signin-code-copy-status as its own element', () => {
      expect(renderer, 'index.ts does not select #signin-code-copy-status').toMatch(/querySelector<HTMLElement>\('#signin-code-copy-status'\)/);
    });

    it('both copy handlers write the confirmation into the near-the-code element after the copy resolves, and no longer route it through the top-of-window announce()', () => {
      for (const name of ['signinCodeCopyButton', 'signinCodeValueElement']) {
        const handlerBlock = renderer.match(new RegExp(`${name}\\.addEventListener\\('click',[\\s\\S]{0,400}?\\}\\);`))?.[0] ?? '';
        expect(handlerBlock, `no click handler block found for ${name}`).not.toBe('');
        const copyIndex = handlerBlock.indexOf('copyCode(');
        expect(copyIndex, `${name}: no copyCode call`).toBeGreaterThan(-1);
        // Whatever variable name index.ts gave the #signin-code-copy-status element, it must be
        // assigned .textContent after the copy call resolves — not before, which would claim the
        // copy happened before the IPC round trip returned.
        const textAssignMatch = handlerBlock.match(/(\w+)\.textContent\s*=\s*(['"`])[^'"`]*copied[^'"`]*\2/i);
        expect(textAssignMatch, `${name}: no .textContent assignment mentioning "copied" found`).not.toBeNull();
        const assignIndex = handlerBlock.indexOf(textAssignMatch![0]);
        expect(assignIndex, `${name}: confirmation text is assigned before copyCode() is called`).toBeGreaterThan(copyIndex);
        // The element the handler writes into must resolve back to #signin-code-copy-status, not
        // #notice/announce() and not #signin-code-status (step 3's countdown element) relabelled.
        const varName = textAssignMatch![1];
        expect(varName, `${name}: writes the confirmation onto noticeElement/announce() instead of the near-the-code element`).not.toBe('noticeElement');
        expect(renderer, `index.ts never binds a variable named ${varName} to #signin-code-copy-status`)
          .toMatch(new RegExp(`const ${varName}\\s*=\\s*document\\.querySelector<HTMLElement>\\('#signin-code-copy-status'\\)`));
      }
    });
  });
});

describe('a new, narrow clipboard-copy IPC channel carries the code — not the unrelated Support Report channel', () => {
  it('shared/preload-contract.ts names a distinct channel for it', () => {
    expect(preloadContract, 'IPC has no authCodeCopy channel').toMatch(/authCodeCopy:\s*'auth:code-copy'/);
  });

  it('shared/contract.ts types and validates the request as a plain code string, not the SupportRequest shape', () => {
    // copySupportReport (src/main/support-report.ts) builds its own report from a
    // {runtime, recoveryCode} context — it has no way to carry an arbitrary code string, so this
    // has to be its own request type, not a repurposed SupportRequest.
    expect(contract, 'TerminalApi.auth has no copyCode method').toMatch(/copyCode\(code:\s*string\)\s*:\s*Promise</);
  });

  it('preload exposes auth.copyCode over the new channel, not by reaching for navigator.clipboard directly', () => {
    expect(preload, 'preload does not invoke IPC.authCodeCopy').toMatch(/ipcRenderer\.invoke\(IPC\.authCodeCopy/);
    expect(preload).not.toMatch(/navigator\.clipboard/);
  });

  it('the main-process handler writes the code through the existing clipboard.writeText path used by support-report copying, not a new mechanism', () => {
    const handlerBlock = mainSource.match(/ipcMain\.handle\(IPC\.authCodeCopy,[\s\S]{0,400}?\}\);/)?.[0] ?? '';
    expect(handlerBlock, 'no ipcMain.handle(IPC.authCodeCopy, ...) registered in src/main/index.ts').not.toBe('');
    expect(handlerBlock, 'authCodeCopy handler does not call clipboard.writeText').toMatch(/clipboard\.writeText\(/);
    // Same authority check every other renderer-facing handler in this file already applies —
    // a handler that skips it would accept the call from any webContents, not just the app's own
    // renderer.
    expect(handlerBlock, 'authCodeCopy handler does not call assertRenderer').toMatch(/assertRenderer\(/);
  });
});

describe('the countdown reads M:SS, not raw seconds', () => {
  it('renderAuthScreens formats the remaining time through formatCountdown, imported from auth-view', () => {
    expect(renderer).toMatch(/from ['"]\.\/auth-view['"]/);
    expect(renderer, 'index.ts does not reference formatCountdown').toContain('formatCountdown');
    const renderFn = renderer.match(/function renderAuthScreens\(\): void \{[\s\S]{0,2000}?\n\}/)?.[0] ?? '';
    // formatCountdown must be called with the computed `remaining` value (from
    // codeSecondsRemaining), not a literal — a lazy fix could satisfy "the function is called
    // somewhere" while feeding it a hardcoded number.
    expect(renderFn, 'renderAuthScreens does not call formatCountdown(remaining)').toMatch(/formatCountdown\(\s*remaining\s*\)/);
  });

  it('drops the old raw-seconds template entirely — "583s." is gone, not just supplemented', () => {
    expect(renderer).not.toMatch(/Expires in \$\{remaining\}s\./);
    expect(renderer).not.toMatch(/`Expires in /);
  });

  it('the waiting-step status text names "left" only when a countdown value actually exists, never appended to an empty/undefined result', () => {
    const renderFn = renderer.match(/function renderAuthScreens\(\): void \{[\s\S]{0,2000}?\n\}/)?.[0] ?? '';
    // The unknown-lifetime case (auth-view-unknown-lifetime.test.ts, formatCountdown(undefined)
    // === '') must never reach the screen as "left" with nothing in front of it — that would be
    // exactly the "fabricated moment" the pre-existing comment in this function already warns
    // against for the expired case.
    expect(renderFn).toMatch(/remaining\s*===\s*undefined/);
  });
});

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
const css = readFileSync(new URL('../src/renderer/index.css', import.meta.url), 'utf8');

// The exact markup region this file cares about — everything the redesigned #signin-code screen
// owns. Falls back to '' so every assertion below fails loudly (not with a cryptic regex miss)
// if the container itself is missing or was renamed.
const codeSection = html.match(/<div id="signin-code"[^>]*>[\s\S]*?<\/div>\s*(?=<div id="signin-ready")/)?.[0] ?? '';

// A click handler as written in index.ts: everything from the addEventListener line down to the
// `});` that closes it at column 0. Deliberately not the "first `});`" the older assertions in
// this file use — a `.then(() => { ... });` continuation ends in `});` too, and stopping there
// would cut off exactly the part that matters now.
const clickHandler = (name: string): string =>
  renderer.match(new RegExp(`${name}\\.addEventListener\\('click',[\\s\\S]{0,900}?\\n\\}\\);`))?.[0] ?? '';

// The label the source actually puts on the button — a string literal, or an identifier bound to
// one somewhere in the file. Resolving the identifier keeps `const COPIED = 'Copied'` from being
// treated as "no label set", without accepting an arbitrary expression nobody can read here.
const labelValue = (expression: string): string | undefined => {
  const trimmed = expression.trim().replace(/;$/, '').trim();
  const literal = trimmed.match(/^(['"`])([^'"`]*)\1$/);
  if (literal) return literal[2];
  if (!/^[A-Za-z_$][\w$]*$/.test(trimmed)) return undefined;
  return renderer.match(new RegExp(`const ${trimmed}\\s*=\\s*(['"\`])([^'"\`]*)\\1`))?.[2];
};

const labelAssignments = (source: string): { text: string; value: string | undefined }[] =>
  [...source.matchAll(/signinCodeCopyButton\.textContent\s*=\s*([^;\n]+)/g)]
    .map((match) => ({ text: match[0], value: labelValue(match[1]) }));

const copiedAssignment = (): string | null =>
  labelAssignments(renderer).find((assignment) => assignment.value === 'Copied')?.text ?? null;

// True when the segment sets the button label to "Copied" itself, or calls a function in this
// file that does. The indirection is allowed on purpose: both trigger points sharing one helper
// is the natural way to write this, and a test that forbade it would push the implementation to
// duplicate the same three lines twice.
const reachesCopiedLabel = (segment: string): boolean => {
  if (labelAssignments(segment).some((assignment) => assignment.value === 'Copied')) return true;
  for (const call of segment.matchAll(/\b([A-Za-z_$][\w$]*)\s*\(/g)) {
    const body = renderer.match(new RegExp(`function ${call[1]}\\([\\s\\S]{0,600}?\\n\\}`))?.[0]
      ?? renderer.match(new RegExp(`const ${call[1]}\\s*=\\s*\\([\\s\\S]{0,600}?\\n\\}`))?.[0]
      ?? '';
    if (body && labelAssignments(body).some((assignment) => assignment.value === 'Copied')) return true;
  }
  return false;
};

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

  // OWNER CHANGED THE DECISION (2026-08-23), this is not a fit to whatever got implemented.
  // The previous round of this file required the opposite: a dedicated #signin-code-copy-status
  // element living inside step 2's <li>, selected by index.ts, written to after the copy resolved.
  // That element is cancelled. The confirmation moves into the Copy button itself: on a successful
  // copy the button's own label becomes "Copied" for a short while and then goes back to "Copy".
  // Nothing else on the screen says anything about the copy.
  //
  // Two things follow, and both are pinned below because each has an obvious cheap fake:
  //   * the element is *gone*, not hidden — markup, renderer and stylesheet all have to lose it;
  //   * the label *comes back*, so a second copy is legible as a second copy. A label that latches
  //     on "Copied" forever is indistinguishable from a button that did nothing the second time.
  describe('the separate confirmation line is gone from the screen, not merely hidden', () => {
    it('index.html no longer carries #signin-code-copy-status anywhere', () => {
      expect(html, 'index.html still ships the cancelled #signin-code-copy-status element').not.toMatch(/signin-code-copy-status/);
    });

    it('step 2 contains only the code and the Copy button — no second element left over to hold a confirmation under another name', () => {
      const step2 = codeSection.match(/<li id="signin-step-code"[\s\S]*?<\/li>/)?.[0] ?? '';
      expect(step2, 'could not locate #signin-step-code').not.toBe('');
      // Renaming #signin-code-copy-status to #signin-copy-feedback would pass the assertion above
      // while leaving exactly the line the owner cancelled on the screen. Pinning the id set, not
      // one id, closes that.
      const ids = (step2.match(/id="([^"]+)"/g) ?? []).map((attr) => attr.slice(4, -1));
      expect(ids.sort(), `step 2 carries unexpected elements: ${ids.join(', ')}`)
        .toEqual(['signin-code-copy', 'signin-code-value', 'signin-step-code']);
      // A live region with no id would be the same line again, invisible to the id check.
      expect(step2, 'step 2 still carries a role="status" live region — the cancelled confirmation line under another guise').not.toMatch(/role="status"/);
    });

    it('index.css keeps no rule for the removed element — a rule that survives is either dead code or the element hidden by styles instead of deleted', () => {
      expect(css, 'index.css still has a #signin-code-copy-status rule').not.toMatch(/signin-code-copy-status/);
    });

    it('index.ts no longer references the removed element at all', () => {
      expect(renderer, 'index.ts still selects or writes to #signin-code-copy-status').not.toMatch(/signin-code-copy-status/i);
      expect(renderer, 'index.ts still keeps the signinCodeCopyStatusElement binding').not.toMatch(/signinCodeCopyStatusElement/);
    });

    it('the confirmation is not pushed into the top banner or into step 3\'s countdown line instead', () => {
      // Both are already on screen and already wired, so both are one-line ways to keep a
      // confirmation somewhere while ignoring the instruction that it belongs in the button.
      expect(renderer, 'the countdown element (#signin-code-status, step 3) is used to announce the copy')
        .not.toMatch(/signinCodeStatusElement\.textContent\s*=\s*[^;]*[Cc]opied/);
      for (const name of ['signinCodeCopyButton', 'signinCodeValueElement']) {
        const handlerBlock = clickHandler(name);
        expect(handlerBlock, `${name}: the copy handler announces through the top-of-window banner`).not.toMatch(/announce\(|noticeElement/);
      }
      // Catches the confirmation being re-created as a fresh element under a new id anywhere on
      // the screen — including outside step 2, where the id-set assertion above cannot see it.
      // The only thing in index.ts allowed to say "copied" is the button's own label.
      for (const assignment of renderer.matchAll(/(\w+)\.textContent\s*=\s*(['"`])[^'"`]*[Cc]opied[^'"`]*\2/g)) {
        expect(assignment[1], `${assignment[0]} — the copy confirmation is written onto an element other than the Copy button`).toBe('signinCodeCopyButton');
      }
    });
  });

  describe('the Copy button says "Copied" once the copy has actually happened, and then says "Copy" again', () => {
    it('the button ships as "Copy" in the markup — the confirmation is a state, not the resting label', () => {
      const step2 = codeSection.match(/<li id="signin-step-code"[\s\S]*?<\/li>/)?.[0] ?? '';
      const label = step2.match(/<button[^>]*id="signin-code-copy"[^>]*>([^<]*)<\/button>/)?.[1] ?? '';
      expect(label.trim(), 'the Copy button does not ship with the label "Copy"').toBe('Copy');
    });

    it('index.ts sets the button\'s own label to "Copied"', () => {
      expect(copiedAssignment(), 'nothing in index.ts assigns "Copied" to signinCodeCopyButton.textContent — the confirmation never reaches the button')
        .not.toBeNull();
    });

    it('both trigger points — the Copy button and the code itself — put the label up only after copyCode() resolves', () => {
      for (const name of ['signinCodeCopyButton', 'signinCodeValueElement']) {
        const handlerBlock = clickHandler(name);
        expect(handlerBlock, `no click handler block found for ${name}`).not.toBe('');
        const copyIndex = handlerBlock.indexOf('copyCode(');
        expect(copyIndex, `${name}: no copyCode call`).toBeGreaterThan(-1);
        const tail = handlerBlock.slice(copyIndex);
        // Setting the label on the next statement after the call would relabel the button while
        // the IPC round trip is still in flight — the button would say "Copied" even when the
        // main-process clipboard write rejects. The continuation has to be sequenced on the
        // promise.
        expect(/\.then\(|await\s/.test(tail), `${name}: the label change is not sequenced after the copyCode() promise — it happens while the copy is still in flight`).toBe(true);
        expect(reachesCopiedLabel(tail), `${name}: nothing on the resolved path of copyCode() sets the button label to "Copied"`).toBe(true);
      }
    });

    it('the label is put back to "Copy" on a timer, not left latched on "Copied"', () => {
      const assignment = copiedAssignment();
      expect(assignment, 'no "Copied" assignment to start from').not.toBeNull();
      // The restore has to live on the same path, close to the assignment that set it — a
      // "Copy" literal somewhere else in the file (the markup default, an unrelated helper) is
      // not a restore.
      const region = renderer.slice(renderer.indexOf(assignment!), renderer.indexOf(assignment!) + 600);
      expect(region, 'the "Copied" label is never scheduled to go away — the button latches and a second copy looks like nothing happened')
        .toMatch(/setTimeout\(/);
      expect(labelAssignments(region).some((each) => each.value === 'Copy'), 'nothing on that path assigns the button label back to "Copy"').toBe(true);
      const delay = Number(region.match(/setTimeout\([\s\S]{0,300}?,\s*(\d+)\s*\)/)?.[1] ?? NaN);
      expect(Number.isFinite(delay), 'the restore timer has no literal delay to check').toBe(true);
      // setTimeout(..., 0) technically restores the label and is worth nothing: the confirmation
      // is gone before the next frame paints.
      expect(delay, `the "Copied" label is restored after ${delay}ms — too short to be read`).toBeGreaterThanOrEqual(500);
    });

    it('a second copy does not get its confirmation cut short by the first copy\'s timer', () => {
      // Copy, wait ~1s, copy again: the first timer is still pending and fires shortly after the
      // second copy, wiping a confirmation that is a moment old. The timer handle has to be kept
      // and cleared before a new one is armed.
      const assignment = copiedAssignment();
      expect(assignment, 'no "Copied" assignment to start from').not.toBeNull();
      const start = renderer.indexOf(assignment!);
      // Scoped to the restore timer itself: index.ts already has unrelated setTimeout/clearTimeout
      // pairs in the production probes, and matching those would pass this test without a restore
      // timer existing at all.
      const region = renderer.slice(Math.max(0, start - 400), start + 600);
      const handleMatch = region.match(/(\w+)\s*=\s*setTimeout\(/);
      expect(handleMatch, 'the restore timer\'s handle is not kept anywhere, so it can never be cancelled').not.toBeNull();
      const handle = handleMatch![1];
      expect(renderer, `nothing clears ${handle} before arming a new restore timer — overlapping copies cut each other's confirmation short`)
        .toMatch(new RegExp(`clearTimeout\\(\\s*${handle}`));
    });

    it('the keyboard path into the copy is still the click handler, so Enter/Space gets the same button feedback', () => {
      const keyBlock = renderer.match(/signinCodeValueElement\.addEventListener\('keydown',[\s\S]{0,400}?\n\}\);/)?.[0] ?? '';
      expect(keyBlock, 'the Enter/Space handler on #signin-code-value is gone').not.toBe('');
      expect(keyBlock).toMatch(/event\.key !== 'Enter'/);
      expect(keyBlock).toMatch(/event\.key !== ' '/);
      expect(keyBlock, 'the keyboard path no longer goes through the click handler, so it would skip the button feedback')
        .toMatch(/signinCodeValueElement\.click\(\)/);
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

import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// The screen half of the desktop wallet line — second panel on void-code#76, G3; moved by the third
// (C2, 85). The decision is exercised for real in tests/auth-view-wallet-line.test.ts
// (walletLineFor); this file pins where the line lives and how index.ts keeps it current.
//
// Round 2 put #wallet-line inside #signin-ready, inside #preflight — and render() hides #preflight
// whenever a chat is selected. workspace.selectedId persists across restarts, so a returning user
// opens straight into a chat and never sees the balance at all. The rule now:
//
//  - #wallet-line lives outside #preflight and #signin-ready, in a container that stays on screen
//    whatever the chat selection: no ancestor ships `hidden`, index.ts never hides any of them, and
//    none of them is <main>, where the terminal draws over everything else;
//  - index.ts sets its text from walletLineFor and hides it when walletLineFor says null — and for
//    no other reason, so it is never hidden because a chat is selected;
//  - the status is read at load and again whenever a chat launch starts, so the line does not show
//    the morning's balance all day (vc already asks /v1/vc/me on every launch);
//  - the launch notice is never shown here.
//
// Same approach as tests/access-not-granted-screen.test.ts: index.ts has DOM side effects on import
// and no DOM environment is configured, so the wiring is pinned as text against the markup and the
// source. Honest limits: it cannot prove the line renders or that anyone can read it (CSS could still
// push it off screen) — that needs a real Electron run; and it recognises a status refresh only as a
// top-level `function` declaration that reaches applyAuthStatus, the convention index.ts follows.

const html = readFileSync(new URL('../src/renderer/index.html', import.meta.url), 'utf8');
const renderer = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');

const LINE_ID = 'wallet-line';

const lineElement = html.match(new RegExp(`<(\\w+)[^>]*\\bid="${LINE_ID}"[^>]*>([\\s\\S]*?)</\\1>`));

// --- markup: the open elements around an id, by a small tag-stack walk over index.html ----------

const VOID_ELEMENTS = new Set(['area', 'base', 'br', 'col', 'embed', 'hr', 'img', 'input', 'link', 'meta', 'source', 'track', 'wbr']);
interface OpenElement { tag: string; id?: string; attrs: string }
function ancestorsOf(id: string): OpenElement[] | null {
  const stack: OpenElement[] = [];
  for (const [, closing, rawTag, attrs] of html.matchAll(/<(\/?)([a-zA-Z][\w-]*)\b([^>]*)>/g)) {
    const tag = rawTag.toLowerCase();
    if (closing) {
      const at = stack.map((open) => open.tag).lastIndexOf(tag);
      if (at >= 0) stack.length = at;
      continue;
    }
    const own = attrs.match(/\bid="([^"]*)"/)?.[1];
    if (own === id) return [...stack];
    if (VOID_ELEMENTS.has(tag) || attrs.trim().endsWith('/')) continue;
    stack.push({ tag, id: own, attrs });
  }
  return null;
}
const describeElement = (open: OpenElement): string => (open.id ? `<${open.tag} id="${open.id}">` : `<${open.tag}>`);
const shipsHidden = (open: OpenElement): boolean => /(?:^|\s)hidden(?:[\s=/]|$)/.test(open.attrs);

// --- source: bindings, functions, and what reaches applyAuthStatus -------------------------------

const escape = (text: string): string => text.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');

// The renderer's binding for a selector, resolved from the source rather than assumed.
const bindingForSelector = (selector: string): string =>
  renderer.match(new RegExp(`const (\\w+)\\s*=\\s*document\\.querySelector(?:<[^>]*>)?\\(['"]${escape(selector)}['"]\\)`))?.[1] ?? '';
const bindingFor = (id: string): string => bindingForSelector(`#${id}`);

// Every way index.ts could take an element off screen.
function hidingWrites(binding: string): string[] {
  const b = escape(binding);
  return [
    new RegExp(`\\b${b}\\.hidden\\s*=(?!=)[^;]*;`, 'g'),
    new RegExp(`\\b${b}\\.(?:toggleAttribute|setAttribute)\\(\\s*['"]hidden['"][^;]*;`, 'g'),
    new RegExp(`\\b${b}\\.style\\.(?:display|visibility)\\s*=[^;]*;`, 'g'),
    new RegExp(`\\b${b}\\.remove\\(\\)`, 'g'),
  ].flatMap((pattern) => [...renderer.matchAll(pattern)].map(([write]) => write));
}

// The top-level function whose body holds `index` (top-level functions start at column 0).
function enclosingFunction(index: number): string {
  const before = renderer.slice(0, index);
  const matches = [...before.matchAll(/^(?:async )?function (\w+)\(/gm)];
  return matches.at(-1)?.[1] ?? '';
}
function functionBody(name: string): string {
  return renderer.match(new RegExp(`^(?:async )?function ${name}\\([^)]*\\)[^{]*\\{[\\s\\S]*?\\n\\}`, 'm'))?.[0] ?? '';
}
const topLevelFunctions = [...renderer.matchAll(/^(?:async )?function (\w+)\(/gm)].map(([, name]) => name);

// applyAuthStatus, and every top-level function that reaches it through other top-level functions:
// loadAuthStatus and recheckAuthStatus today.
function statusRefreshers(): Set<string> {
  const reached = new Set(['applyAuthStatus']);
  for (let grew = true; grew;) {
    grew = false;
    for (const name of topLevelFunctions) {
      if (reached.has(name) || name === 'launch') continue;
      const body = functionBody(name).replace(/^[^{]*\{/, '');
      if ([...reached].some((callee) => new RegExp(`\\b${callee}\\(`).test(body))) { reached.add(name); grew = true; }
    }
  }
  return reached;
}
const callsARefresher = (code: string): string | undefined =>
  [...statusRefreshers()].find((name) => new RegExp(`\\b${name}\\(`).test(code));

// The `{…}` block that opens at `open`, braces counted (index.ts has none inside its strings here).
function blockFrom(code: string, open: number): string {
  let depth = 0;
  for (let at = open; at < code.length; at++) {
    if (code[at] === '{') depth++;
    else if (code[at] === '}' && --depth === 0) return code.slice(open, at + 1);
  }
  return code.slice(open);
}
// launch()'s body without its failure paths: the catch block and the onExit callback both re-read
// status already, and neither runs when a chat starts well.
function launchStartPath(): { body: string; stripped: string } {
  const body = functionBody('launch');
  let stripped = body;
  for (const pattern of [/\}\s*catch\s*(?:\([^)]*\))?\s*\{/, /\bonExit\([^,]*,\s*(?:async\s*)?\([^)]*\)\s*=>\s*\{/]) {
    const match = pattern.exec(stripped);
    if (!match) continue;
    const open = match.index + match[0].length - 1;
    stripped = stripped.slice(0, open) + stripped.slice(open + blockFrom(stripped, open).length);
  }
  return { body, stripped };
}

// A local that holds walletLineFor's answer, or walletLineFor( itself.
const fedFromWalletLineFor = (expression: string): boolean =>
  /\bwalletLineFor\(/.test(expression)
  || [...expression.matchAll(/\b([A-Za-z_]\w*)\b/g)].some(([, name]) => new RegExp(`\\b${name}\\s*=\\s*walletLineFor\\(`).test(renderer));

describe('the wallet line lives where a returning user sees it', () => {
  const ancestors = ancestorsOf(LINE_ID);

  it(`index.html carries #${LINE_ID}`, () => {
    expect(ancestors, `index.html has no #${LINE_ID}`).not.toBeNull();
  });

  it(`#${LINE_ID} is not inside #preflight or #signin-ready — render() hides #preflight whenever a chat is selected`, () => {
    const ids = (ancestors ?? []).map((open) => open.id);
    expect(ids, `#${LINE_ID} sits inside #signin-ready: a returning user, reopened into a chat, never sees the balance`).not.toContain('signin-ready');
    expect(ids, `#${LINE_ID} sits inside #preflight, which index.ts hides once workspace.selectedId is set`).not.toContain('preflight');
    expect(ids).not.toContain('signin-panel');
  });

  it(`no container of #${LINE_ID} ships hidden`, () => {
    expect(ancestors, `index.html has no #${LINE_ID}`).not.toBeNull();
    const hidden = (ancestors ?? []).filter(shipsHidden).map(describeElement);
    expect(hidden, `#${LINE_ID} sits inside ${hidden.join(' > ')}, hidden in the markup`).toEqual([]);
  });

  it(`index.ts never hides a container of #${LINE_ID}`, () => {
    expect(ancestors, `index.html has no #${LINE_ID}`).not.toBeNull();
    const offenders = (ancestors ?? [])
      .filter((open) => open.tag !== 'html' && open.tag !== 'body')
      .map((open) => ({ open, binding: open.id ? bindingFor(open.id) : bindingForSelector(open.tag) }))
      .filter(({ binding }) => binding !== '')
      .flatMap(({ open, binding }) => hidingWrites(binding).map((write) => `${describeElement(open)} via ${write}`));
    expect(offenders, `#${LINE_ID} goes off screen with its container:\n  ${offenders.join('\n  ')}`).toEqual([]);
  });

  it(`#${LINE_ID} is not inside <main>, where the running chat's terminal covers everything else`, () => {
    expect(ancestors, `index.html has no #${LINE_ID}`).not.toBeNull();
    expect((ancestors ?? []).map((open) => open.tag)).not.toContain('main');
  });

  it('ships empty — no placeholder money before vc has answered', () => {
    expect(lineElement, `index.html has no closed #${LINE_ID} element`).not.toBeNull();
    const inner = (lineElement?.[2] ?? '').replace(/<[^>]*>/g, '').trim();
    expect(inner, `#${LINE_ID} ships with text of its own: "${inner}"`).toBe('');
  });
});

describe('index.ts fills it from walletLineFor on every status read', () => {
  const binding = bindingFor(LINE_ID);

  it(`selects #${LINE_ID}`, () => {
    expect(binding, `index.ts never selects #${LINE_ID}, so the markup is dead weight`).not.toBe('');
  });

  it('imports walletLineFor from auth-view — the decision is not re-made inline', () => {
    const importLine = renderer.match(/import \{[^}]*\} from '\.\/auth-view';/)?.[0] ?? '';
    expect(importLine, 'index.ts does not import walletLineFor from ./auth-view').toMatch(/\bwalletLineFor\b/);
  });

  it(`sets #${LINE_ID}'s text from walletLineFor, as text`, () => {
    expect(binding, `index.ts never selects #${LINE_ID}`).not.toBe('');
    const assignment = renderer.match(new RegExp(`${binding}\\.textContent\\s*=([^;]*);`));
    expect(assignment, `index.ts never sets ${binding}.textContent`).not.toBeNull();
    expect(fedFromWalletLineFor(assignment?.[1] ?? ''), `${binding}.textContent is not set from walletLineFor: ${assignment?.[0]}`).toBe(true);
    expect(renderer, `${binding} is written as HTML — vc's line is text`).not.toMatch(new RegExp(`${binding}\\.innerHTML\\s*=`));
  });

  it(`hides #${LINE_ID} when walletLineFor says null, and for no other reason`, () => {
    expect(binding, `index.ts never selects #${LINE_ID}`).not.toBe('');
    const writes = hidingWrites(binding);
    expect(writes, `index.ts never hides ${binding} — an empty line stays on screen as a blank`).not.toEqual([]);
    for (const write of writes) {
      expect(fedFromWalletLineFor(write), `${write} is not decided by walletLineFor`).toBe(true);
      expect(write, `${write} hides the balance because of the chat selection`).not.toMatch(/\b(?:selectedId|workspace|preflight\w*|signinOnStartFailure)\b/);
    }
  });

  it('refreshes with the status: the line is set in applyAuthStatus or renderAuthScreens, or in a helper they call', () => {
    expect(binding, `index.ts never selects #${LINE_ID}`).not.toBe('');
    const at = renderer.search(new RegExp(`${binding}\\.textContent\\s*=`));
    expect(at, `index.ts never sets ${binding}.textContent`).toBeGreaterThanOrEqual(0);
    const owner = enclosingFunction(at);
    const statusPaths = ['applyAuthStatus', 'renderAuthScreens'];
    const reached = statusPaths.includes(owner)
      || statusPaths.some((name) => new RegExp(`\\b${owner}\\(`).test(functionBody(name)));
    expect(reached, `${binding}.textContent is set in ${owner || 'top-level code'}, which no status read reaches — a recheck would leave yesterday's balance on screen`).toBe(true);
  });
});

describe('the status is read again whenever a chat launch starts', () => {
  it('reads it once at load: a top-level statement runs a status refresh', () => {
    const refreshers = [...statusRefreshers()].filter((name) => name !== 'applyAuthStatus');
    const atLoad = refreshers.some((name) => new RegExp(`^(?:void |await )?${name}\\(\\)`, 'm').test(renderer));
    expect(atLoad, `no top-level statement calls any of ${refreshers.join(', ')} — the window opens without a balance`).toBe(true);
  });

  it('launch() refreshes the status on its way to starting the chat, not only when it fails', () => {
    const { body, stripped } = launchStartPath();
    expect(body, 'could not find function launch in index.ts').not.toBe('');
    expect(stripped.length, 'could not separate launch()\'s failure paths from its start path').toBeLessThan(body.length);
    const refresher = callsARefresher(stripped.replace(/^[^{]*\{/, ''));
    expect(refresher, `launch() re-reads the status only in its catch block and onExit callback (${[...statusRefreshers()].join(', ')}) — `
      + 'a chat started at noon leaves the morning\'s balance in the header').toBeDefined();
  });
});

describe('the launch notice is not shown on the desktop', () => {
  it('index.ts never reads launchNotice — the warning stays inside Pi, docked above its editor', () => {
    expect(renderer).not.toMatch(/\blaunchNotice\b/);
  });
});

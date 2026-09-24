import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// The screen half of the desktop wallet line — second panel on void-code#76, G3. The decision is
// exercised for real in tests/auth-view-wallet-line.test.ts (walletLineFor); this file pins that
// the signed-in screen has a place for the line and that index.ts fills it from that decision.
//
// Same approach as tests/access-not-granted-screen.test.ts: index.ts has DOM side effects on import
// and no DOM environment is configured, so the wiring is pinned as text against the markup and the
// source. Honest limit: it cannot prove the line renders or that anyone can read it — that needs a
// real Electron run.

const html = readFileSync(new URL('../src/renderer/index.html', import.meta.url), 'utf8');
const renderer = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');

const LINE_ID = 'wallet-line';

// The signed-in screen: #signin-ready up to the next sibling in #signin-panel.
const readySection = html.match(/<div id="signin-ready"[^>]*>[\s\S]*?<\/div>(?=\s*<(?:div id="signin-|button id="signin-start|p id="signin-status))/)?.[0] ?? '';
const lineElement = html.match(new RegExp(`<(\\w+)[^>]*\\bid="${LINE_ID}"[^>]*>([\\s\\S]*?)</\\1>`));

// The renderer's binding for an id, resolved from the source rather than assumed.
const bindingFor = (id: string): string =>
  renderer.match(new RegExp(`const (\\w+)\\s*=\\s*document\\.querySelector<[^>]*>\\('#${id}'\\)`))?.[1] ?? '';

// The top-level function whose body holds `index` (top-level functions start at column 0).
function enclosingFunction(index: number): string {
  const before = renderer.slice(0, index);
  const matches = [...before.matchAll(/^(?:async )?function (\w+)\(/gm)];
  return matches.at(-1)?.[1] ?? '';
}
function functionBody(name: string): string {
  return renderer.match(new RegExp(`^(?:async )?function ${name}\\([^)]*\\)[^{]*\\{[\\s\\S]*?\\n\\}`, 'm'))?.[0] ?? '';
}

describe('the signed-in screen has a place for the wallet line', () => {
  it(`index.html carries #${LINE_ID} inside #signin-ready`, () => {
    expect(readySection, 'could not locate #signin-ready in index.html').not.toBe('');
    expect(readySection, `#signin-ready has no #${LINE_ID} — the signed-in screen has nowhere to show the balance`).toMatch(new RegExp(`\\bid="${LINE_ID}"`));
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
    const rhs = assignment?.[1] ?? '';
    // Directly, or through a local that holds walletLineFor's answer.
    const fed = /\bwalletLineFor\(/.test(rhs)
      || [...rhs.matchAll(/\b([A-Za-z_]\w*)\b/g)].some(([, name]) => new RegExp(`\\b${name}\\s*=\\s*walletLineFor\\(`).test(renderer));
    expect(fed, `${binding}.textContent is not set from walletLineFor: ${assignment?.[0]}`).toBe(true);
    expect(renderer, `${binding} is written as HTML — vc's line is text`).not.toMatch(new RegExp(`${binding}\\.innerHTML\\s*=`));
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

describe('the launch notice is not shown on the desktop', () => {
  it('index.ts never reads launchNotice — the warning stays inside Pi, docked above its editor', () => {
    expect(renderer).not.toMatch(/\blaunchNotice\b/);
  });
});

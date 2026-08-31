import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { appVersionLabel } from '../src/renderer/app-version';

// "Which version have you got?" is the first question asked of anyone with a
// problem, and today the app cannot answer it. The version is in no screen at
// all: the only place it appears is inside the Support Report, which is behind
// a button, produces a JSON blob, and is the thing you ask for AFTER you know
// what you are looking at.
//
// So the version goes in the header, beside the product name -- on screen from
// the moment the window opens, before a folder is chosen, before sign-in, in
// every state the app has. That is the proposal; the shape below is what a test
// can hold onto:
//
//   <header><strong>Void Code</strong><span id="app-version">…</span>…</header>
//
// WHAT THIS FILE CAN AND CANNOT DO. There is no DOM environment in this
// project's vitest config (`vitest.config.ts` declares none, and neither jsdom
// nor happy-dom is installed), and src/renderer/index.ts side-effects on import
// against a real IPC bridge -- so nothing here renders anything. The same split
// the renderer already uses is used again: the DECISION lives in a pure module
// and is measured for real, and the WIRING is read as text. See
// renderer-login.test.ts, which tests auth-view.ts exactly this way.
//
// The half that is measured is the half that would silently produce a blank
// space: appVersionLabel must return something visible for every input,
// including the inputs a broken bridge produces.

const read = (relative: string): string => readFileSync(new URL(`../${relative}`, import.meta.url), 'utf8');
const html = read('src/renderer/index.html');
const css = read('src/renderer/index.css');
const renderer = read('src/renderer/index.ts');
const preload = read('src/preload/index.ts');
const preloadContract = read('src/shared/preload-contract.ts');
const main = read('src/main/index.ts');

describe('the label is never empty, whatever it is handed', () => {
  it('shows a released version as a version', () => {
    expect(appVersionLabel('0.2.50')).toBe('v0.2.50');
  });

  it('shows a branch build as the branch build it is', () => {
    // A build off the tag must not be able to pass itself off as the release --
    // that is the same lie as 0.1.0 with better manners.
    expect(appVersionLabel('0.2.50-3-gabc1234')).toBe('v0.2.50-3-gabc1234');
  });

  it('does not double the v when handed a stamp that already carries one', () => {
    expect(appVersionLabel('v0.2.50')).toBe('v0.2.50');
  });

  const empties: ReadonlyArray<readonly [string, unknown]> = [
    ['an empty string', ''],
    ['whitespace', '   '],
    ['undefined, which is what a bridge that answered nothing gives', undefined],
    ['null', null],
    ['a number', 250],
    ['an object', {}],
  ];

  it.each(empties)('says so out loud when handed %s', (_name, value) => {
    // The failure mode being blocked: the header renders, the span is there,
    // and it contains nothing -- so the app looks like it never had a version
    // and nobody can tell that from "the version could not be read".
    const label = appVersionLabel(value as string);
    expect(label.trim().length, 'the label is blank, so the version silently disappears from the screen').toBeGreaterThan(0);
    expect(label).not.toMatch(/undefined|null|\[object/);
    expect(label.toLowerCase()).toMatch(/unknown|unavailable/);
  });

  it('never returns a blank label for any input at all', () => {
    for (const value of ['', ' ', '\n', 'dev', 'vc dev', '0.0.0-gabc1234', undefined, null, 0, NaN, [], {}]) {
      expect(appVersionLabel(value as string).trim()).not.toBe('');
    }
  });
});

describe('the version has a place on screen, and it is not behind a button', () => {
  const header = /<header[^>]*>([\s\S]*?)<\/header>/.exec(html)?.[1] ?? '';

  it('index.html has a header to put it in', () => {
    expect(header.length, 'index.html no longer has a <header> element').toBeGreaterThan(0);
  });

  it('carries #app-version inside that header', () => {
    expect(header, 'the header does not contain an element with id="app-version"').toMatch(/id="app-version"/);
  });

  it('does not hide it, and does not put it inside the Support panel', () => {
    // #support-panel carries `hidden` and is opened by a button. A version
    // there is a version nobody sees, which is the state being left behind.
    const support = /<section id="support-panel"[\s\S]*?<\/section>/.exec(html)?.[0] ?? '';
    expect(support).not.toMatch(/id="app-version"/);
    const element = /<[a-z]+[^>]*id="app-version"[^>]*>/.exec(html)?.[0] ?? '';
    expect(element, 'no element declares id="app-version"').not.toBe('');
    expect(element, 'the version element is born hidden').not.toMatch(/\bhidden\b/);
  });

  it('is not hidden by stylesheet either', () => {
    // An element that exists and is `display: none` satisfies every check above
    // and shows nothing. This is the cheap version of looking at the screen.
    const rule = new RegExp(String.raw`#app-version[^{]*\{[^}]*\}`, 'g');
    for (const declaration of css.match(rule) ?? []) {
      expect(declaration, `#app-version is hidden by CSS: ${declaration}`).not.toMatch(/display\s*:\s*none|visibility\s*:\s*hidden|content-visibility\s*:\s*hidden/);
      expect(declaration, `#app-version is sized away by CSS: ${declaration}`).not.toMatch(/font-size\s*:\s*0\b|opacity\s*:\s*0\b/);
    }
  });
});

describe('what lands in that element is the real version, carried from main', () => {
  it('main answers the question, using the version electron-builder stamped', () => {
    // app.getVersion() is the packaged bundle's own version, which is what
    // `-c.extraMetadata.version` sets at packaging time. Reading package.json
    // from disk instead would report 0.1.0 forever -- the in-tree placeholder
    // is exactly what is NOT the answer.
    expect(main, 'src/main/index.ts registers no handler that returns app.getVersion()').toMatch(/ipcMain\.handle\(IPC\.appVersion[\s\S]{0,200}app\.getVersion\(\)/);
  });

  it('the channel is declared once, in the shared contract', () => {
    expect(preloadContract, 'IPC has no appVersion channel').toMatch(/\bappVersion:\s*'[^']+'/);
  });

  it('the bridge exposes it to the renderer', () => {
    expect(preload, 'the preload bridge does not expose appVersion').toMatch(/appVersion[\s\S]{0,120}IPC\.appVersion/);
  });

  it('the renderer puts it into #app-version through the label function', () => {
    expect(renderer, 'index.ts does not read #app-version').toMatch(/app-version/);
    expect(renderer, 'index.ts does not use appVersionLabel').toMatch(/appVersionLabel/);
    expect(renderer, 'index.ts does not ask the bridge for the version').toMatch(/appVersion/);
  });

  it('the renderer does not carry a version literal of its own', () => {
    // A hardcoded '0.1.0' in the renderer would satisfy every check above and
    // reintroduce the original defect at the last possible moment.
    const assignments = renderer.match(/['"`]v?\d+\.\d+\.\d+[^'"`]*['"`]/g) ?? [];
    expect(assignments.join(', ') || 'no version literal in the renderer').toBe('no version literal in the renderer');
  });
});

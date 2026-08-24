import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Measured directly (see the task report): the code-screen countdown ticks correctly once a
// second while the window is focused, but stalls while the window is backgrounded — and this
// screen is looked at *from the background*, because signing in means leaving the app to type
// the code into a browser. The person comes back and the clock has jumped, or (worse) an
// isCodeExpired check computed from a stale `codeStartedAt` disagrees with what the screen last
// showed. Chromium's renderer-side timer throttling in backgrounded windows is the documented
// cause (src/main/index.ts:152 creates the window without disabling it); there is no Electron
// runtime in this test suite to observe a real throttled interval, so the fix is pinned as source
// text: the BrowserWindow this app actually shows must ask Chromium not to throttle it.
//
// Honest limit: this proves the constructor option is present in the object literal that builds
// the shown window. It cannot prove Chromium actually honors the flag at runtime, or that the
// countdown keeps ticking in a real backgrounded window — that needs a real Electron run, which
// nothing in this test suite has.

const mainSource = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');

describe('the main window disables Chromium\'s background timer throttling', () => {
  it('locates the createWindow() BrowserWindow construction', () => {
    const ctor = mainSource.match(/new BrowserWindow\(\{[\s\S]*?\}\)\)/)?.[0] ?? '';
    expect(ctor, 'could not locate `new BrowserWindow({...}))` in src/main/index.ts').not.toBe('');
  });

  it('passes backgroundThrottling: false to the BrowserWindow that is actually shown to the person', () => {
    // Scoped to the one BrowserWindow this app creates and shows (createWindow(), which sets
    // mainWindow) — not any other electron.BrowserWindow-shaped literal that might exist for a
    // headless/smoke path, which nobody watches a countdown in.
    const createWindowFn = mainSource.match(/async function createWindow\(\)[\s\S]*?\n\}/)?.[0] ?? '';
    expect(createWindowFn, 'could not locate createWindow() in src/main/index.ts').not.toBe('');
    const ctor = createWindowFn.match(/new BrowserWindow\(\{[\s\S]*?\}\)\)/)?.[0] ?? '';
    expect(ctor, 'createWindow() does not construct a BrowserWindow').not.toBe('');
    expect(ctor, 'the shown BrowserWindow does not set backgroundThrottling: false').toMatch(/backgroundThrottling\s*:\s*false/);
  });
});

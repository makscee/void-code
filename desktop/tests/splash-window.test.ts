import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { splashWindowOptions } from '../src/main/splash-window';

// The splash window exists because a cold Windows start takes up to 331 seconds. That number is
// also the reason its options are a decision and not a detail: whatever this window is, a person
// looks at it for minutes, on top of whatever else they were doing. And resolvePrivateRuntime has
// no timeout, so "for minutes" can become "until they give up" — at which point the window has to
// be something they can deal with by hand, without the app's help.
//
// Honest limit: this pins the option object the app builds, not Electron's behaviour with it.
// Nothing in this suite runs a real Electron window.

type Options = Record<string, unknown> & { webPreferences?: Record<string, unknown> };

const options = (): Options => splashWindowOptions() as Options;

const splashSource = readFileSync(new URL('../src/main/splash-window.ts', import.meta.url), 'utf8');

describe('the startup splash window', () => {
  it('leaves the person in control of their screen: it neither floats above their work nor hides from the taskbar', () => {
    const built = options();
    // Anchor first, so the two absence checks below cannot pass on an empty or stubbed object:
    // an options literal that has lost its geometry is not an options literal we know how to read.
    expect(built.width, 'splashWindowOptions() did not return a window options object').toBeTypeOf('number');
    // Absent is as good as false (Electron defaults both to false); anything else — including a
    // truthy non-boolean — is the trap coming back, and names itself in the failure.
    const capturing = ['alwaysOnTop', 'skipTaskbar'].filter((key) => built[key] !== undefined && built[key] !== false);
    expect(capturing, 'a splash the person cannot dismiss: it stays on top of, or missing from, everything else').toEqual([]);
  });

  it('is on screen from the moment it is created, which is the whole reason it exists', () => {
    expect(options().show).toBe(true);
  });

  it('paints itself in the instants before the page is on screen', () => {
    const built = options();
    expect(built.width).toBeTypeOf('number');
    expect(built.height).toBeTypeOf('number');
    expect(built.backgroundColor).toBe('#101216');
  });

  it('gives a throwaway window no more renderer privilege than the main one', () => {
    const preferences = options().webPreferences ?? {};
    expect(preferences.sandbox).toBe(true);
    expect(preferences.contextIsolation).toBe(true);
    expect(preferences.nodeIntegration).toBe(false);
  });

  it('builds the window people actually see from those options, with no second set written inline', () => {
    // Without this, the options function can be entirely correct while createSplashWindow keeps
    // its own literal — the tests above would pass and the trap would ship. Pinned as source text
    // because constructing the real BrowserWindow needs an Electron runtime this suite has not got.
    const ctor = splashSource.match(/new BrowserWindow\([\s\S]*?\);/)?.[0] ?? '';
    expect(ctor, 'could not locate `new BrowserWindow(...)` in src/main/splash-window.ts').not.toBe('');
    expect(ctor, 'the shown splash window is not built from splashWindowOptions()').toMatch(/splashWindowOptions\(\)/);
    expect(ctor, 'the shown splash window sets window options inline, bypassing splashWindowOptions()').not.toMatch(/alwaysOnTop|skipTaskbar/);
  });
});

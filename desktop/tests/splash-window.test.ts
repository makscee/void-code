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

const splashPage = readFileSync(new URL('../src/renderer/splash.html', import.meta.url), 'utf8');
const packagedCheck = readFileSync(new URL('../scripts/packaged-window-check.mjs', import.meta.url), 'utf8');

describe('the startup splash page', () => {
  it('tells the person what closing this window costs them, because closing it cancels the start', () => {
    // The splash is the only window for almost the whole start (the main one is created last),
    // so `window-all-closed` quits the app whenever it is closed -- at five seconds into a normal
    // thirteen-second start just as much as into a hung one. Cancelling deliberately is fine; the
    // page promising "closes by itself once the app is ready" and saying nothing of the rest is not.
    //
    // Honest limit, and it is the whole weakness of this test: meaning is not checkable here. What
    // is pinned is a marked element plus a vocabulary -- so rewording survives, deleting the
    // warning fails, and a sentence that uses these words to say something else would still pass.
    // A stronger version would need a reader, which no test in this suite has.
    const element = /<([a-z]+)\b[^>]*\bdata-role=["']close-cancels-startup["'][^>]*>([\s\S]*?)<\/\1>/i.exec(splashPage);
    const warning = (element?.[2] ?? '').replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim();
    expect(warning, 'splash.html carries no element marked data-role="close-cancels-startup"').not.toBe('');
    expect(warning, 'the warning does not mention closing this window').toMatch(/clos/i);
    expect(warning, 'the warning does not say that closing cancels the start').toMatch(/cancel|stop|quit|exit|abort|abandon|interrupt|end|(?:won.t|will not|never) start/i);
  });
});

describe('the packaged window check and the splash share one size threshold', () => {
  it('measures the main window by a threshold the splash is bound to stay under, not by a number that happens to match', async () => {
    // packaged-window-check.mjs tells the main window from the splash by size alone. Nothing but
    // coincidence keeps 460x260 under its `>= 500`: grow the splash to 500x300 and the check does
    // not fail, it quietly starts measuring the splash and passing. So the threshold has to be one
    // value both sides read -- then either side moving breaks this test for real.
    //
    // Imported dynamically so a missing module reds this test alone instead of the whole file.
    const { mainWindowMinimumEdge } = await import('../scripts/window-thresholds.mjs');
    expect(mainWindowMinimumEdge, 'window-thresholds.mjs does not export a numeric mainWindowMinimumEdge').toBeTypeOf('number');

    const built = options();
    expect(built.width, 'the splash is wide enough to be mistaken for the main window').toBeLessThan(mainWindowMinimumEdge as number);
    expect(built.height, 'the splash is tall enough to be mistaken for the main window').toBeLessThan(mainWindowMinimumEdge as number);

    // The other half of the link. Pinned as source text because packaged-window-check.mjs cannot be
    // imported to be asked -- it opens a socket, makes a temp directory and launches the packaged
    // app at module scope. Honest limit: this proves where the number comes from, not that the
    // check behaves.
    expect(packagedCheck, 'packaged-window-check.mjs does not read the shared threshold').toMatch(/from\s+['"]\.\/window-thresholds\.mjs['"]/);
    expect(packagedCheck, 'packaged-window-check.mjs still compares a window dimension against a number of its own').not.toMatch(/\b(?:width|height)\s*[<>]=?\s*\d/);
  });
});

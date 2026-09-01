// rails:pin-on-coverage пин на уже верное поведение: краснеть было не с чего, реализация есть и должна быть. Ценность доказана мутациями, не краснотой: убран backgroundColor, #101216 -> #ffffff, sandbox:false, contextIsolation:false, nodeIntegration:true, удалён блок webPreferences — шесть подстановок, шесть смертей, каждая валит ровно свой тест. Седьмая (переименование createWindow) проверяет, что пин не позеленеет молча на пустой строке.
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// Two properties of the one window this app now has. Both were pinned before, and neither was
// pinned for this window: they were pinned for the loading window, in tests/splash-window.test.ts,
// which went away with the second window (428fac1). What was checked was a copy of these settings;
// the originals have never been held by anything.
//
// Source text, for the same reason background-throttling.test.ts is source text: createWindow() is
// not exported and builds a real BrowserWindow, and this suite has no Electron to build it in.
//
// Honest limit, stated the same way there: this proves the option is present in the literal the app
// constructs its window from. It cannot prove Electron honours it -- that a renderer really is
// sandboxed, or that the window really paints that colour before the page arrives.

const mainSource = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');

function windowConstructor(): string {
  const createWindowFn = mainSource.match(/async function createWindow\(\)[\s\S]*?\n\}/)?.[0] ?? '';
  expect(createWindowFn, 'could not locate createWindow() in src/main/index.ts').not.toBe('');
  const ctor = createWindowFn.match(/new BrowserWindow\(\{[\s\S]*?\}\)\)/)?.[0] ?? '';
  expect(ctor, 'createWindow() does not construct a BrowserWindow').not.toBe('');
  return ctor;
}

describe('the window the application actually opens', () => {
  it('paints itself dark before the page is on screen', () => {
    // The first thing a person sees on a cold start, for as long as it takes the loading page to
    // arrive. Without it the window opens white -- and a white rectangle is where the whole of
    // yesterday's investigation started. The value is pinned, not merely its presence: any other
    // colour is the same flash in a different shade.
    expect(windowConstructor(), 'the window is constructed with no backgroundColor, so it opens white until the page paints').toMatch(/backgroundColor\s*:\s*'#101216'/);
  });

  it('gives its renderer no more privilege than it had when there were two windows', () => {
    // This matters more now than it did. One window means the preload is attached to the loading
    // page too, and `window.voidTerminal` is declared in it. Nothing can be called through it yet
    // -- the handlers are not registered, and rendererAuthority is bound to index.html and refuses
    // -- but that is a property of the ordering, and these three are what stand behind it.
    const preferences = /webPreferences\s*:\s*\{([^}]*)\}/.exec(windowConstructor())?.[1] ?? '';
    expect(preferences, 'the window is constructed with no webPreferences block').not.toBe('');
    expect(preferences, 'the renderer is not sandboxed').toMatch(/\bsandbox\s*:\s*true/);
    expect(preferences, 'the renderer shares a context with the preload').toMatch(/\bcontextIsolation\s*:\s*true/);
    expect(preferences, 'the renderer is given Node').toMatch(/\bnodeIntegration\s*:\s*false/);
  });
});

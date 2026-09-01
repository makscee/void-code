// rails:pin-on-coverage пин на уже верное поведение: спиннер работал, а закреплён не был — обе мутации (animation:none и удаление элемента) выживали при 851 зелёном тесте. Ценность доказана мутациями: animation:none, элемент удалён, длительность 0s, удалён блок @keyframes, страница перестала быть находимой — пять подстановок, пять смертей.
import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';

// The spinner is the one moving thing on this screen, and during a 331-second cold start it is the
// only evidence the person has that the app is alive rather than wedged. Nothing was holding it:
// mutation showed that `animation: none` and deleting the element outright both survived the whole
// suite, 851 tests green.
//
// Honest limit: this proves the element is on the page and that the stylesheet gives it an
// animation with a real duration and real keyframes. It cannot prove Chromium turns it. That takes
// two frames off a live machine, which the spec requires separately and no test here can stand in
// for.

const renderer = new URL('../src/renderer/', import.meta.url);

// The page is found by what it says, not by what it is called. The single-window change renames the
// carrier (splash.html stops being a splash), and a test pinned to a filename would either break on
// the rename or quietly start reading nothing.
function loadingPage(): { file: string; markup: string; styles: string } {
  const directory = path.dirname(new URL('x', renderer).pathname);
  const candidates = readdirSync(directory).filter((name) => name.endsWith('.html'))
    .map((name) => ({ file: name, markup: readFileSync(path.join(directory, name), 'utf8') }))
    .filter((page) => /data-role=["']close-cancels-startup["']/.test(page.markup));
  expect(candidates.map((page) => page.file), 'exactly one renderer page should be the loading page -- the one that tells the person closing cancels the start').toHaveLength(1);
  const page = candidates[0];
  // Both carriers count: a linked sheet and an inline <style> block are equally real to the browser,
  // and the page already declares its colours twice on purpose.
  const inline = [...page.markup.matchAll(/<style[^>]*>([\s\S]*?)<\/style>/gi)].map((match) => match[1]).join('\n');
  const linked = [...page.markup.matchAll(/<link[^>]+rel=["']stylesheet["'][^>]*>/gi)]
    .map((tag) => /href=["']([^"']+)["']/.exec(tag[0])?.[1])
    .filter((href): href is string => typeof href === 'string' && !/^[a-z]+:/i.test(href))
    .map((href) => readFileSync(path.join(directory, href), 'utf8')).join('\n');
  return { file: page.file, markup: page.markup, styles: `${inline}\n${linked}` };
}

// Everything in an `animation` shorthand that is a timing or a keyword rather than the keyframes
// name. Whatever survives this sieve is what @keyframes has to be called.
const ANIMATION_KEYWORDS = new Set(['normal', 'reverse', 'alternate', 'alternate-reverse', 'none', 'forwards', 'backwards', 'both', 'running', 'paused', 'infinite', 'linear', 'ease', 'ease-in', 'ease-out', 'ease-in-out', 'step-start', 'step-end']);

describe('the loading page keeps something moving', () => {
  it('shows a spinner the stylesheet actually animates, with keyframes that exist', () => {
    const page = loadingPage();

    expect(page.markup, `${page.file} has no element carrying class="spinner"`).toMatch(/class=["'][^"']*\bspinner\b[^"']*["']/);

    const rule = /(?:^|})[^{}]*\.spinner\b[^{}]*\{([^}]*)\}/.exec(page.styles);
    expect(rule?.[1], 'the stylesheet has no .spinner rule to animate it').toBeTypeOf('string');
    const declarations = rule?.[1] ?? '';

    const animation = /animation(?:-name)?\s*:\s*([^;]+)/.exec(declarations)?.[1]?.trim() ?? '';
    expect(animation, 'the .spinner rule declares no animation').not.toBe('');
    expect(animation, 'the spinner is declared and then switched off').not.toMatch(/\bnone\b/);

    // A duration of zero is a spinner that renders one frame and stops -- indistinguishable, to the
    // person waiting, from the frozen window this page replaced.
    const duration = /(\d*\.?\d+)\s*(ms|s)\b/.exec(`${animation} ${declarations}`);
    expect(duration, 'the spinner animation has no duration').not.toBeNull();
    const milliseconds = Number(duration?.[1]) * (duration?.[2] === 's' ? 1_000 : 1);
    expect(milliseconds, 'the spinner animation lasts no time at all').toBeGreaterThan(0);

    const name = animation.split(/\s+/).find((token) => !ANIMATION_KEYWORDS.has(token) && !/^\d*\.?\d+(ms|s)$/.test(token) && !/^(cubic-bezier|steps)\(/.test(token));
    expect(name, 'the spinner animation names no keyframes').toBeTypeOf('string');
    expect(page.styles, `the spinner animates "${name}" but no @keyframes ${name} exists, so nothing moves`).toMatch(new RegExp(`@keyframes\\s+${name}\\b`));
  });

  // Migrated here from tests/splash-window.test.ts when the second window was removed. The window
  // changed; the promise the page makes did not. Closing the window during startup still ends the
  // start -- it is the app's only window, and it always was during this part of the run -- so the
  // page is still obliged to say so before a person finds out by doing it.
  it('tells the person what closing this window costs them, because closing it cancels the start', () => {
    // Honest limit, and it is the whole weakness of this test: meaning is not checkable here. What
    // is pinned is a marked element plus a vocabulary -- so rewording survives, deleting the
    // warning fails, and a sentence that uses these words to say something else would still pass.
    // A stronger version would need a reader, which no test in this suite has.
    const page = loadingPage();
    const element = /<([a-z]+)\b[^>]*\bdata-role=["']close-cancels-startup["'][^>]*>([\s\S]*?)<\/\1>/i.exec(page.markup);
    const warning = (element?.[2] ?? '').replace(/<[^>]*>/g, ' ').replace(/\s+/g, ' ').trim();
    expect(warning, `${page.file} marks an element data-role="close-cancels-startup" but it says nothing`).not.toBe('');
    expect(warning, 'the warning does not mention closing this window').toMatch(/clos/i);
    expect(warning, 'the warning does not say that closing cancels the start').toMatch(/cancel|stop|quit|exit|abort|abandon|interrupt|end|(?:won.t|will not|never) start/i);
  });
});

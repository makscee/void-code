import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// This test is DOCUMENTARY, not behavioural, and that is deliberate.
//
// vitest runs the renderer under happy-dom, which does not enforce
// Content-Security-Policy at all: no meta tag is honoured, no violation is
// raised, nothing observable changes when the policy is wrong. So there is no
// behaviour here to assert against. The only place the policy is really
// enforced is Chromium inside the packaged app — the packaged checks
// (`npm run smoke:packaged`, `scripts/production-terminal-check.mjs`) are what
// can actually catch a terminal that fails to paint.
//
// What this test does instead is pin the *shape* of the policy so that the two
// opposing pressures on it stay visible to whoever edits it next:
//
//   1. Styles must stay permissive, or the terminal is unusable (see below).
//   2. Scripts must stay strict — that is the half that actually matters for
//      security, and it must not be relaxed along with the styles.
//
// It asserts properties, not the literal policy string, so unrelated additions
// (a new img-src source, a font-src, a connect-src) do not turn it red.

// --- WHY style-src MUST allow inline styles -------------------------------
//
// xterm.js does not ship its geometry and colours as static CSS. It creates
// <style> elements at runtime and writes CSS into their textContent as the
// terminal measures itself and as the palette is used:
//
//   * the character-cell dimensions (cell width/height, screen element size)
//     — without them the terminal has no geometry;
//   * the per-colour rules (`.xterm-fg-257 { color: … }`) — without them the
//     text has no colour;
//   * the scrollbar slider styling.
//
// Under `style-src 'self'` every one of those writes is blocked. The result on
// a real machine is a black rectangle with a cursor and nothing else: reported
// from a Windows user's build, with the browser console naming exactly those
// xterm code paths.
//
// This was invisible on macOS because `desktop/src/renderer/terminal-stack.ts`
// defaults to the DOM renderer and *silently* falls back to it when WebGL
// fails to load (`activateProductRenderer` swallows the error). Where WebGL
// comes up, a canvas paints the text and the missing CSS barely shows. Where
// it does not, the DOM renderer needs that CSS and there is none.
//
// Neither of the strict alternatives can work here:
//   * a hash cannot be pinned — xterm rewrites that CSS at runtime, with
//     content that depends on the measured font and the live palette;
//   * a nonce cannot be attached — xterm creates the <style> elements itself,
//     so there is no point at which we could stamp one on.
//
// If you are here to "tighten this up": tightening style-src is what broke the
// terminal in the first place. Tighten script-src instead — it is already as
// tight as it goes, and the assertions below exist to keep it that way.

type Policy = Map<string, string[]>;

const readPolicy = (file: string): Policy => {
  const html = readFileSync(new URL(`../src/renderer/${file}`, import.meta.url), 'utf8');
  const match = /<meta\s+http-equiv="Content-Security-Policy"\s+content="([^"]*)"/i.exec(html);
  expect(match, `${file} must declare a Content-Security-Policy meta tag`).not.toBeNull();
  const policy: Policy = new Map();
  for (const directive of match![1].split(';')) {
    const [name, ...values] = directive.trim().split(/\s+/).filter(Boolean);
    if (name) policy.set(name.toLowerCase(), values);
  }
  return policy;
};

// script-src has no fallback worth trusting here: if it is absent the page
// inherits default-src, so read it through the same fallback the browser uses.
const effective = (policy: Policy, directive: string): string[] =>
  policy.get(directive) ?? policy.get('default-src') ?? [];

describe('renderer Content-Security-Policy', () => {
  describe('index.html — the app window', () => {
    const policy = readPolicy('index.html');

    it('allows the inline styles xterm writes at runtime, or the terminal cannot paint', () => {
      // Cell geometry and text colour arrive as <style> textContent written by
      // xterm while the terminal runs. Blocking them leaves a black rectangle.
      expect(effective(policy, 'style-src')).toContain("'unsafe-inline'");
    });

    it('keeps script-src strict — relaxing styles must not drag scripts along', () => {
      // This is the assertion that matters for security. Loosening styles is
      // cosmetic; loosening scripts turns any injected string into code.
      const scriptSrc = effective(policy, 'script-src');
      expect(scriptSrc).toEqual(["'self'"]);
      expect(scriptSrc).not.toContain("'unsafe-inline'");
      expect(scriptSrc).not.toContain("'unsafe-eval'");
    });

    it('keeps default-src at self', () => {
      expect(policy.get('default-src')).toEqual(["'self'"]);
    });

    it('loads nothing from off the disk — no remote origin in any directive', () => {
      // A tempting "fix" for a missing asset is to allow a CDN. The renderer is
      // bundled and shipped inside the app; nothing here should reach the network.
      for (const [directive, values] of policy) {
        for (const value of values) {
          expect(value, `${directive} must not allow a remote origin (${value})`).not.toMatch(/^(https?:|\/\/|\*)/);
        }
      }
    });
  });

  describe('smoke.html — the packaged fixture page', () => {
    const policy = readPolicy('smoke.html');

    it('keeps script-src strict, exactly as the app window does', () => {
      // The two pages carry separate policies, so they can drift apart. The part
      // that must never drift is the strict half.
      const scriptSrc = effective(policy, 'script-src');
      expect(scriptSrc).toEqual(["'self'"]);
      expect(scriptSrc).not.toContain("'unsafe-inline'");
      expect(scriptSrc).not.toContain("'unsafe-eval'");
    });

    it('keeps default-src at self', () => {
      expect(policy.get('default-src')).toEqual(["'self'"]);
    });

    it('needs no style relaxation only for as long as it renders no terminal', () => {
      // smoke.html drives the IPC layer through window.voidTerminal and never
      // constructs an xterm Terminal, so the xterm reasoning above does not
      // apply to it and its styles stay strict. That is a fact about smoke.ts,
      // not a permanent property — so tie the two together: the day the smoke
      // page starts rendering a real terminal, this fails and points at the CSP
      // that would otherwise leave the fixture painting a black rectangle.
      const smoke = readFileSync(new URL('../src/renderer/smoke.ts', import.meta.url), 'utf8');
      const rendersTerminal = /@xterm\/xterm|terminal-stack|createProductTerminal/.test(smoke);
      if (rendersTerminal) {
        expect(effective(policy, 'style-src'), 'smoke.ts now renders a terminal, so smoke.html must allow inline styles too').toContain("'unsafe-inline'");
      } else {
        expect(effective(policy, 'style-src')).not.toContain("'unsafe-inline'");
      }
    });
  });
});

import { readFileSync } from 'node:fs';
import type * as ProductModule from '../src/renderer/terminal-stack';
import { afterAll, beforeAll, describe, expect, it, vi } from 'vitest';

type Rgba = { r: number; g: number; b: number; a: number };

let product: ProductModule;
let terminal: ReturnType<ProductModule['createProductTerminal']>;

const parseColor = (value: string): Rgba => {
  if (value === 'transparent') return { r: 0, g: 0, b: 0, a: 0 };
  const hex = /^#([\da-f]{6})([\da-f]{2})?$/i.exec(value);
  if (hex) return {
    r: Number.parseInt(hex[1].slice(0, 2), 16),
    g: Number.parseInt(hex[1].slice(2, 4), 16),
    b: Number.parseInt(hex[1].slice(4, 6), 16),
    a: hex[2] ? Number.parseInt(hex[2], 16) / 255 : 1,
  };
  const rgb = /^rgba?\(\s*([\d.]+)[,\s]+\s*([\d.]+)[,\s]+\s*([\d.]+)(?:\s*[,/]\s*([\d.]+)%?)?\s*\)$/i.exec(value);
  if (!rgb) throw new Error(`Unsupported explicit scrollbar color: ${value}`);
  return { r: Number(rgb[1]), g: Number(rgb[2]), b: Number(rgb[3]), a: rgb[4] === undefined ? 1 : Number(rgb[4]) / (value.includes('%') ? 100 : 1) };
};

const luminance = ({ r, g, b }: Rgba): number => {
  const linear = (channel: number) => {
    const normalized = channel / 255;
    return normalized <= 0.04045 ? normalized / 12.92 : ((normalized + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * linear(r) + 0.7152 * linear(g) + 0.0722 * linear(b);
};

const composited = (foreground: Rgba, background: Rgba): Rgba => ({
  r: foreground.r * foreground.a + background.r * (1 - foreground.a),
  g: foreground.g * foreground.a + background.g * (1 - foreground.a),
  b: foreground.b * foreground.a + background.b * (1 - foreground.a),
  a: 1,
});

const contrast = (first: Rgba, second: Rgba): number => {
  const [lighter, darker] = [luminance(first), luminance(second)].sort((a, b) => b - a);
  return (lighter + 0.05) / (darker + 0.05);
};

beforeAll(async () => {
  // terminal-stack reads the renderer query at module evaluation time. Supply a
  // plain, inert location so this test exercises the product module in Vitest's
  // node environment rather than failing before its theme is evaluated.
  vi.stubGlobal('location', new URL('https://renderer.test/'));
  product = await import('../src/renderer/terminal-stack');
  terminal = product.createProductTerminal();
});

afterAll(() => {
  terminal?.terminal.dispose();
  vi.unstubAllGlobals();
});

describe('product xterm scrollbar theme', () => {
  const slider = (field: keyof ProductModule['TERMINAL_THEME']): Rgba => {
    const value = product.TERMINAL_THEME[field];
    expect(value, `${String(field)} must be explicit; xterm otherwise uses its foreground default`).toBeDefined();
    return parseColor(value!);
  };

  it('makes the idle slider explicitly fully transparent instead of accepting xterm’s 20% foreground default', () => {
    expect(slider('scrollbarSliderBackground').a).toBe(0);
  });

  it('makes the hover slider explicitly visible but low-contrast against the #0f1117 terminal background', () => {
    const hover = slider('scrollbarSliderHoverBackground');
    expect(product.TERMINAL_THEME.background).toBe('#0f1117');
    const background = parseColor(product.TERMINAL_THEME.background!);
    const text = parseColor(product.TERMINAL_THEME.foreground!);
    expect(hover.a).toBeGreaterThan(0);
    expect(contrast(composited(hover, background), background)).toBeLessThan(2);
    expect(contrast(composited(hover, background), background)).toBeLessThan(contrast(text, background));
  });

  it('makes the active slider no less visible than hover without becoming a bright opaque foreground bar', () => {
    const active = slider('scrollbarSliderActiveBackground');
    const hover = slider('scrollbarSliderHoverBackground');
    const background = parseColor(product.TERMINAL_THEME.background!);
    const text = parseColor(product.TERMINAL_THEME.foreground!);
    const activeContrast = contrast(composited(active, background), background);
    expect(active.a).toBeGreaterThan(0);
    expect(activeContrast).toBeGreaterThanOrEqual(contrast(composited(hover, background), background));
    expect(activeContrast).toBeLessThan(3);
    expect(activeContrast).toBeLessThan(contrast(text, background));
  });

  it('passes the exported product theme through TERMINAL_OPTIONS into the Terminal it constructs', () => {
    expect(product.TERMINAL_OPTIONS.theme).toBe(product.TERMINAL_THEME);
    expect(terminal.terminal.options.theme).toBe(product.TERMINAL_THEME);
  });

  it('does not hide Recent Chats or every native scrollbar with a CSS workaround', () => {
    const css = readFileSync(new URL('../src/renderer/index.css', import.meta.url), 'utf8');
    expect(css).toMatch(/#recent-list\{[^}]*overflow-y:auto/);
    expect(css).not.toMatch(/::-webkit-scrollbar/);
    expect(css).not.toMatch(/(?:scrollbar-width\s*:\s*none|-ms-overflow-style\s*:\s*none)/);
    for (const [, declarations] of css.matchAll(/\*\s*\{([^}]*)\}/g)) {
      expect(declarations).not.toMatch(/overflow(?:-[xy])?\s*:\s*hidden/);
    }
    expect(css).not.toMatch(/#recent-list\{[^}]*?(?:display\s*:\s*none|visibility\s*:\s*hidden|overflow\s*:\s*hidden)/);
  });
});

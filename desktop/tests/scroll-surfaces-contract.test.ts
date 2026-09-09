import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const css = readFileSync(new URL('../src/renderer/index.css', import.meta.url), 'utf8');

function declarations(selector: string): string {
  const escaped = selector.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  const match = new RegExp(`${escaped}\\{([^}]*)\\}`).exec(css);
  expect(match, `missing product rule for ${selector}`).not.toBeNull();
  return match![1];
}

function property(block: string, name: string): string | undefined {
  return block.split(';').map((entry) => entry.trim()).find((entry) => entry.startsWith(`${name}:`))?.slice(name.length + 1).trim();
}

describe('desktop scroll surface contracts (fast source guard)', () => {
  it('suppresses only xterm’s redundant native viewport scroller, not its real custom scroll surface', () => {
    expect(property(declarations('.xterm .xterm-viewport'), 'overflow-y')).toBe('hidden');
    expect(css).not.toMatch(/\.xterm(?:\s+[^,{]+)*\s+\.xterm-scrollable-element\s*\{[^}]*overflow(?:-[xy])?\s*:\s*hidden/);
    expect(css).not.toMatch(/(?:scrollbar-width\s*:\s*none|-ms-overflow-style\s*:\s*none)/);
  });

  it('lets Recent Chats consume the panel’s minmax row instead of imposing an artificial list cap', () => {
    const recent = declarations('#recent');
    const list = declarations('#recent-list');
    expect(property(recent, 'grid-template-rows')).toBe('auto minmax(0,1fr)');
    expect(property(list, 'min-height')).toBe('0');
    expect(property(list, 'overflow-y')).toBe('auto');
    expect([undefined, 'none']).toContain(property(list, 'max-height'));
  });

  it('requests a stable dark native Recent scrollbar without globally hiding browser scrollbars', () => {
    const list = declarations('#recent-list');
    expect(property(list, 'color-scheme')).toBe('dark');
    const scrollbarColor = property(list, 'scrollbar-color');
    expect(scrollbarColor).toBeDefined();
    expect(scrollbarColor).not.toBe('auto');
    expect(scrollbarColor).toMatch(/^(?:#[\da-f]{3,8}|rgba?\([^)]*\))\s+transparent$/i);
    expect(css).not.toMatch(/::-webkit-scrollbar/);
  });
});

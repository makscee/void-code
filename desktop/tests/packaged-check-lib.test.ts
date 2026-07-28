import { describe, expect, it } from 'vitest';
import { normalizeAsarEntry } from '../scripts/packaged-check-lib.mjs';

describe('packaged check helpers', () => {
  it('normalizes asar entry separators on Windows and POSIX', () => {
    expect(normalizeAsarEntry('\\dist\\renderer\\index.html')).toBe('/dist/renderer/index.html');
    expect(normalizeAsarEntry('/dist/renderer/index.html')).toBe('/dist/renderer/index.html');
  });
});

import { describe, expect, it } from 'vitest';
import { normalizeAsarEntry, startupFailureTimeoutMs } from '../scripts/packaged-check-lib.mjs';

describe('packaged check helpers', () => {
  it('normalizes asar entry separators on Windows and POSIX', () => {
    expect(normalizeAsarEntry('\\dist\\renderer\\index.html')).toBe('/dist/renderer/index.html');
    expect(normalizeAsarEntry('/dist/renderer/index.html')).toBe('/dist/renderer/index.html');
  });

  it('allows Windows runtime validation to finish before timing out startup failure checks', () => {
    expect(startupFailureTimeoutMs(false)).toBe(20_000);
    expect(startupFailureTimeoutMs(true)).toBe(600_000);
  });
});

import { describe, expect, it } from 'vitest';
import { nsisUtf8Environment } from '../scripts/package-windows.mjs';

describe('Windows NSIS package environment', () => {
  it('forces a UTF-8 locale for Linux bundled NSIS argv conversion without altering product metadata', () => {
    expect(nsisUtf8Environment('linux', { LANG: 'C', KEEP: 'yes' })).toEqual({ LANG: 'C.UTF-8', LC_ALL: 'C.UTF-8', KEEP: 'yes' });
  });
  it('leaves non-Linux build environments unchanged', () => {
    const environment = { LANG: 'en_US.UTF-8' };
    expect(nsisUtf8Environment('win32', environment)).toBe(environment);
  });
});

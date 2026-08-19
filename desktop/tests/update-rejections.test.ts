import { describe, expect, it } from 'vitest';
import { evaluateStableManifest } from '../src/main/stable-update';
const accepted = (version: string, overrides: Record<string, unknown> = {}) => ({
  schemaVersion: 1, status: 'accepted', version, platform: 'win32', architecture: 'x64',
  feedUrl: 'https://vc.makscee.ru/download/windows/', artifactUrl: `https://vc.makscee.ru/download/windows/Void-Code-${version}-windows-x64.exe`,
  immutableUrl: `https://github.com/makscee/void-code/releases/download/desktop-v${version}/Void-Code-${version}-windows-x64.exe`,
  sha256: 'a'.repeat(64), sha512: Buffer.alloc(64, 1).toString('base64'), size: 123456, publisherName: 'Test Publisher', publishedAt: '2026-08-17T00:00:00.000Z', ...overrides,
});
describe('stable manifest rejection boundary', () => {
  it.each([
    ['same', accepted('0.1.1'), 'up-to-date'], ['older', accepted('0.1.0'), 'up-to-date'], ['prerelease', accepted('0.2.0-rc.1'), 'unavailable'],
    ['wrong feed', accepted('0.2.0', { feedUrl: 'https://evil.example/' }), 'unavailable'], ['wrong artifact', accepted('0.2.0', { artifactUrl: 'https://evil.example/update.exe' }), 'unavailable'],
    ['wrong immutable', accepted('0.2.0', { immutableUrl: 'https://github.com/other/release.exe' }), 'unavailable'], ['sha256', accepted('0.2.0', { sha256: 'abc' }), 'unavailable'],
    ['sha512', accepted('0.2.0', { sha512: 'abc' }), 'unavailable'], ['publisher absent', accepted('0.2.0', { publisherName: '' }), 'unavailable'],
    ['platform', accepted('0.2.0', { platform: 'darwin' }), 'unavailable'], ['architecture', accepted('0.2.0', { architecture: 'arm64' }), 'unavailable'],
    ['unknown', { ...accepted('0.2.0'), extra: true }, 'unavailable'],
  ])('%s fails closed', (_name, raw, state) => expect(evaluateStableManifest(raw, '0.1.1', 'win32', 'x64').state).toBe(state));
  it('accepts only the exact unavailable union', () => expect(evaluateStableManifest({ schemaVersion: 1, status: 'unavailable' }, '0.1.1', 'win32', 'x64')).toEqual({ state: 'unavailable', currentVersion: '0.1.1', canRetry: true }));
});

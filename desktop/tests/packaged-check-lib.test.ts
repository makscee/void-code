import { describe, expect, it } from 'vitest';
import { assertLocalizationEntries, assertWindowsUpdaterMetadata, normalizeAsarEntry, startupFailureTimeoutMs } from '../scripts/packaged-check-lib.mjs';

describe('packaged check helpers', () => {
  it('normalizes asar entry separators on Windows and POSIX', () => {
    expect(normalizeAsarEntry('\\dist\\renderer\\index.html')).toBe('/dist/renderer/index.html');
    expect(normalizeAsarEntry('/dist/renderer/index.html')).toBe('/dist/renderer/index.html');
  });
  it('requires both local bundles and the localization notice in packaged resources', () => {
    const entries = ['\\dist\\renderer\\l10n\\en.json', '/dist/renderer/l10n/ru.json', '/dist/THIRD_PARTY_NOTICES.md'];
    expect(() => assertLocalizationEntries(entries)).not.toThrow();
    expect(() => assertLocalizationEntries(entries.slice(1))).toThrow(/en\.json/);
  });
  it('allows Windows runtime validation to finish before timing out startup failure checks', () => {
    expect(startupFailureTimeoutMs(false)).toBe(20_000); expect(startupFailureTimeoutMs(true)).toBe(600_000);
  });
  it('requires signed app-update and exact latest full installer metadata for 0.1.2', () => {
    const valid = { appUpdateText: 'provider: generic\nurl: https://vc.makscee.ru/download/windows/\npublisherName:\n  - Test Publisher\n', latestText: 'version: 0.1.2\nfiles:\n  - url: Void-Code-0.1.2-windows-x64.exe\n    sha512: abc=\n    size: 42\npath: Void-Code-0.1.2-windows-x64.exe\nsha512: abc=\nsize: 42\n', version: '0.1.2', installerName: 'Void-Code-0.1.2-windows-x64.exe', size: 42, sha512: 'abc=', expectedPublisherName: 'Test Publisher' };
    expect(() => assertWindowsUpdaterMetadata(valid)).not.toThrow();
    expect(() => assertWindowsUpdaterMetadata({ ...valid, expectedPublisherName: undefined })).toThrow(/expected signer/);
    expect(() => assertWindowsUpdaterMetadata({ ...valid, appUpdateText: 'provider: generic\nurl: https://vc.makscee.ru/download/windows/\n' })).toThrow(/publisher/);
    expect(() => assertWindowsUpdaterMetadata({ ...valid, latestText: valid.latestText.replace(valid.installerName, 'other.exe') })).toThrow(/identity/);
    expect(() => assertWindowsUpdaterMetadata({ ...valid, latestText: valid.latestText.replace('    size: 42\n', '') })).toThrow(/size/);
  });
});

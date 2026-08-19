import { describe, expect, it, vi } from 'vitest';
import { readFileSync } from 'node:fs';
import { parseAppUpdateConfiguration, StableUpdateController, UpdaterRequestAuthorizer, type UpdaterAdapter } from '../src/main/stable-update';
import { installUpdaterRequestBoundary } from '../src/main/electron-updater-adapter';
import { configureLocaleBeforeReady } from '../src/main/locale-store';
import { unavailableUpdateStatus } from '../src/renderer/update-view';
import { persistLocaleSelection } from '../src/renderer/localization';

const version = '0.2.0';
const artifactUrl = `https://vc.makscee.ru/download/windows/Void-Code-${version}-windows-x64.exe`;
const immutableUrl = `https://github.com/makscee/void-code/releases/download/desktop-v${version}/Void-Code-${version}-windows-x64.exe`;
const manifest = { schemaVersion: 1 as const, status: 'accepted' as const, version, platform: 'win32' as const, architecture: 'x64' as const, feedUrl: 'https://vc.makscee.ru/download/windows/', artifactUrl, immutableUrl, sha256: 'a'.repeat(64), sha512: Buffer.alloc(64, 1).toString('base64'), size: 42, publisherName: 'Test Publisher', publishedAt: '2026-08-17T00:00:00.000Z' };

function fakeSession() {
  let beforeRequest!: (details: { url: string; webContentsId?: number }, callback: (reply: { cancel: boolean }) => void) => void;
  let beforeRedirect!: (details: { url: string; redirectURL: string; webContentsId?: number }) => void;
  return {
    session: { webRequest: { onBeforeRequest: vi.fn((_filter, listener) => { beforeRequest = listener; }), onBeforeRedirect: vi.fn((_filter, listener) => { beforeRedirect = listener; }), onHeadersReceived: vi.fn() } },
    request(url: string, webContentsId = -1) { let cancel = false; beforeRequest({ url, webContentsId }, (reply) => { cancel = reply.cancel; }); return !cancel; },
    redirect(url: string, redirectURL: string) { beforeRedirect({ url, redirectURL, webContentsId: -1 }); },
  };
}

describe('review updater request authorization', () => {
  it('attaches to the supplied updater session and consumes the exact two-hop redirect chain', () => {
    const fake = fakeSession(); const authorizer = new UpdaterRequestAuthorizer(); installUpdaterRequestBoundary(fake.session as never, authorizer); authorizer.authorize(manifest);
    expect(fake.request(`https://vc.makscee.ru/download/windows/latest.yml?noCache=${Date.now().toString(32)}`)).toBe(true);
    expect(fake.request(artifactUrl)).toBe(true);
    expect(fake.request(artifactUrl, 7)).toBe(false);
    expect(fake.request(immutableUrl)).toBe(false);
    fake.redirect(artifactUrl, immutableUrl); expect(fake.request(immutableUrl)).toBe(true); expect(fake.request(immutableUrl)).toBe(false);
    const signed = 'https://release-assets.githubusercontent.com/github-production-release-asset/123/abc?sp=r&sig=x';
    fake.redirect(immutableUrl, signed); expect(fake.request(signed)).toBe(true); expect(fake.request(signed)).toBe(false);
    expect(fake.request('https://release-assets.githubusercontent.com/github-production-release-asset/other')).toBe(false);
  });

  it('rejects broad, malformed, or out-of-state requests', () => {
    const policy = new UpdaterRequestAuthorizer(); policy.authorize(manifest);
    for (const url of [
      'https://vc.makscee.ru/download/windows/latest.yml?x=1',
      'https://vc.makscee.ru/download/windows/latest.yml?noCache=NOTBASE32',
      'https://vc.makscee.ru/download/windows/Void-Code-9.9.9-windows-x64.exe',
      'https://github.com/makscee/void-code/releases/download/desktop-v9.9.9/Void-Code-9.9.9-windows-x64.exe',
      'https://user@release-assets.githubusercontent.com/github-production-release-asset/a',
      'https://release-assets.githubusercontent.com:444/github-production-release-asset/a',
      'https://release-assets.githubusercontent.com/github-production-release-asset/a#hash',
    ]) expect(policy.allowRequest(url)).toBe(false);
  });
});

describe('review YAML publisher boundary', () => {
  it('normalizes the standard signed builder array and rejects absent/ambiguous/malformed publishers', () => {
    expect(parseAppUpdateConfiguration('provider: generic\nurl: https://vc.makscee.ru/download/windows/\npublisherName:\n  - Test Publisher\n')).toEqual({ provider: 'generic', url: manifest.feedUrl, publisherNames: ['Test Publisher'] });
    for (const yaml of ['provider: generic\nurl: https://vc.makscee.ru/download/windows/\n', 'provider: generic\nurl: https://vc.makscee.ru/download/windows/\npublisherName: [One, Two]\n', 'provider: generic\nurl: https://vc.makscee.ru/download/windows/\npublisherName: {name: One}\n']) expect(() => parseAppUpdateConfiguration(yaml)).toThrow();
  });
});

describe('review locale and renderer recovery behavior', () => {
  it('configures persisted English before readiness settles', async () => {
    let release!: () => void; const ready = new Promise<void>((resolve) => { release = resolve; }); const configured: string[] = [];
    const pending = configureLocaleBeforeReady('/profile', (locale) => configured.push(locale), () => ready, () => ({ current: () => 'en' as const }) as never);
    expect(configured).toEqual(['en']); release(); await pending;
  });
  it('uses runtime version for updater status failure', () => expect(unavailableUpdateStatus('7.8.9')).toMatchObject({ currentVersion: '7.8.9' }));
  it('restores locale selector and announces localized persistence failure', async () => {
    const select = { value: 'en' }; const announce = vi.fn();
    await persistLocaleSelection(select, 'ru', vi.fn().mockRejectedValue(new Error('disk')), vi.fn(), announce, (message) => `ru:${message}`);
    expect(select.value).toBe('ru'); expect(announce).toHaveBeenCalledWith('ru:Language could not be saved. Try again.');
  });
});

describe('review progress and package identity', () => {
  it('clamps malformed transport progress before publication', async () => {
    let progress!: (value: { percent: number; transferred: number; total: number }) => void;
    const statuses: unknown[] = [];
    const updater = { configure: vi.fn(), authorize: vi.fn(), packageConfiguration: vi.fn().mockResolvedValue({ provider: 'generic', url: manifest.feedUrl, publisherNames: [manifest.publisherName] }), checkForUpdates: vi.fn().mockResolvedValue({ version, files: [{ url: artifactUrl, sha512: manifest.sha512, size: 42 }] }), onProgress: vi.fn((listener) => { progress = listener; }), downloadUpdate: vi.fn(async () => { progress({ percent: Number.POSITIVE_INFINITY, transferred: -10, total: 0 }); return ['/tmp/a']; }), sha256: vi.fn().mockResolvedValue(manifest.sha256), size: vi.fn().mockResolvedValue(42), remove: vi.fn(), cleanupPartials: vi.fn(), cleanupOwnedSessions: vi.fn(), quitAndInstall: vi.fn() } satisfies UpdaterAdapter;
    const body = (async function* () { yield new TextEncoder().encode(JSON.stringify(manifest)); })();
    const subject = new StableUpdateController({ currentVersion: '0.1.1', platform: 'win32', architecture: 'x64', fetch: vi.fn().mockResolvedValue({ ok: true, body }), updater, onStatus: (status) => statuses.push(status) });
    await subject.check(); await subject.updateNow();
    const downloading = statuses.filter((status): status is { state: 'downloading'; percent: number; transferred: number; total: number } => (status as { state?: string }).state === 'downloading');
    expect(downloading).toHaveLength(2); expect(downloading.at(-1)).toMatchObject({ percent: 0, transferred: 0, total: 42 });
  });
  it('uses the dedicated updater net session and does not hard-code stale Electron package identity', () => {
    const adapterSource = readFileSync(new URL('../src/main/electron-updater-adapter.ts', import.meta.url), 'utf8');
    expect(adapterSource).toContain('autoUpdater.netSession'); expect(adapterSource).not.toContain('defaultSession');
    const packageSource = readFileSync(new URL('../scripts/windows-package-check.mjs', import.meta.url), 'utf8');
    expect(packageSource).not.toContain("electron: '39.2.6'"); expect(packageSource).toContain('electronVersion');
  });
});

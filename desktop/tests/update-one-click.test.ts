import { describe, expect, it, vi } from 'vitest';
import { StableUpdateController, type UpdaterAdapter } from '../src/main/stable-update';

const version = '0.2.0';
const artifactUrl = `https://vc.makscee.ru/download/windows/Void-Code-${version}-windows-x64.exe`;
const manifest = {
  schemaVersion: 1, status: 'accepted', version, platform: 'win32', architecture: 'x64',
  feedUrl: 'https://vc.makscee.ru/download/windows/', artifactUrl,
  immutableUrl: `https://github.com/makscee/void-code/releases/download/desktop-v${version}/Void-Code-${version}-windows-x64.exe`,
  sha256: 'a'.repeat(64), sha512: Buffer.alloc(64, 1).toString('base64'), size: 42,
  publisherName: 'Test Publisher', publishedAt: '2026-08-17T00:00:00.000Z',
};
const response = (value: unknown) => ({ ok: true, body: (async function* () { yield new TextEncoder().encode(JSON.stringify(value)); })() });
function adapter(overrides: Partial<UpdaterAdapter> = {}): UpdaterAdapter {
  return {
    configure: vi.fn(), authorize: vi.fn(),
    packageConfiguration: vi.fn().mockResolvedValue({ provider: 'generic', url: manifest.feedUrl, publisherNames: [manifest.publisherName] }),
    checkForUpdates: vi.fn().mockResolvedValue({ version, files: [{ url: artifactUrl, sha512: manifest.sha512, size: 42 }] }),
    onProgress: vi.fn(), downloadUpdate: vi.fn().mockResolvedValue(['/private/cache/update.exe']),
    sha256: vi.fn().mockResolvedValue(manifest.sha256), size: vi.fn().mockResolvedValue(42),
    remove: vi.fn().mockResolvedValue(undefined), cleanupPartials: vi.fn().mockResolvedValue(undefined),
    cleanupOwnedSessions: vi.fn().mockResolvedValue(undefined), quitAndInstall: vi.fn(), ...overrides,
  };
}
function controller(updater = adapter(), raw: unknown = manifest) {
  return new StableUpdateController({ currentVersion: '0.1.1', platform: 'win32', architecture: 'x64',
    fetch: vi.fn().mockResolvedValue(response(raw)), updater });
}

describe('one-click updater core', () => {
  it('checks canonical and generated metadata before downloading, then installs only after independent verification', async () => {
    const updater = adapter(); const subject = controller(updater);
    await expect(subject.check()).resolves.toMatchObject({ state: 'available', availableVersion: version });
    await expect(subject.updateNow()).resolves.toBe(true);
    expect(updater.configure).toHaveBeenCalledWith(expect.objectContaining({ autoDownload: false, autoInstallOnAppQuit: false, allowPrerelease: false, allowDowngrade: false, disableWebInstaller: true, disableDifferentialDownload: true }));
    expect(updater.sha256).toHaveBeenCalledBefore(updater.cleanupOwnedSessions as ReturnType<typeof vi.fn>);
    expect(updater.cleanupOwnedSessions).toHaveBeenCalledBefore(updater.quitAndInstall as ReturnType<typeof vi.fn>);
    expect(updater.quitAndInstall).toHaveBeenCalledWith(true, true);
  });

  it.each([
    ['absent packaged publisher', { packageConfiguration: vi.fn().mockResolvedValue({ provider: 'generic', url: manifest.feedUrl, publisherNames: [] }) }],
    ['ambiguous packaged publisher', { packageConfiguration: vi.fn().mockResolvedValue({ provider: 'generic', url: manifest.feedUrl, publisherNames: [manifest.publisherName, 'Other'] }) }],
    ['mismatched publisher', { packageConfiguration: vi.fn().mockResolvedValue({ provider: 'generic', url: manifest.feedUrl, publisherNames: ['Other'] }) }],
    ['wrong feed', { packageConfiguration: vi.fn().mockResolvedValue({ provider: 'generic', url: 'https://evil.example/' , publisherNames: [manifest.publisherName] }) }],
    ['latest version mismatch', { checkForUpdates: vi.fn().mockResolvedValue({ version: '0.3.0', files: [{ url: artifactUrl, sha512: manifest.sha512, size: 42 }] }) }],
    ['multiple latest files', { checkForUpdates: vi.fn().mockResolvedValue({ version, files: [{ url: artifactUrl, sha512: manifest.sha512, size: 42 }, { url: artifactUrl, sha512: manifest.sha512, size: 42 }] }) }],
    ['missing latest size', { checkForUpdates: vi.fn().mockResolvedValue({ version, files: [{ url: artifactUrl, sha512: manifest.sha512 }] }) }],
    ['wrong latest artifact', { checkForUpdates: vi.fn().mockResolvedValue({ version, files: [{ url: 'https://evil.example/update.exe', sha512: manifest.sha512, size: 42 }] }) }],
  ])('fails closed for %s', async (_name, overrides) => {
    const subject = controller(adapter(overrides));
    expect((await subject.check()).state).toBe('unavailable');
  });
});

describe('download security and lifecycle', () => {
  it.each([['size', { size: vi.fn().mockResolvedValue(41) }], ['sha256', { sha256: vi.fn().mockResolvedValue('b'.repeat(64)) }]])('deletes artifact on %s mismatch and remains retryable', async (_name, overrides) => {
    const updater = adapter(overrides); const subject = controller(updater); await subject.check();
    await expect(subject.updateNow()).resolves.toBe(false);
    expect(updater.remove).toHaveBeenCalledWith('/private/cache/update.exe');
    expect(subject.status()).toMatchObject({ state: 'failed', canRetry: true });
    expect(updater.quitAndInstall).not.toHaveBeenCalled();
  });

  it('keeps checks inert while an update download is in flight', async () => {
    let resolveDownload!: (files: string[]) => void;
    const updater = adapter({ downloadUpdate: vi.fn(() => new Promise((resolve) => { resolveDownload = resolve; })) });
    const subject = controller(updater); await subject.check();
    const updating = subject.updateNow();
    const current = subject.status();

    await expect(subject.check()).resolves.toBe(current);
    expect(subject.status()).toBe(current);
    expect(updater.checkForUpdates).toHaveBeenCalledTimes(1);

    resolveDownload(['/private/cache/update.exe']);
    await expect(updating).resolves.toBe(true);
  });

  it('reauthorizes consumed requests when retrying an in-app download failure without browser fallback', async () => {
    let budget = 0; let downloads = 0;
    const updater = adapter({
      authorize: vi.fn((authorized) => { budget = authorized ? 1 : 0; }),
      checkForUpdates: vi.fn(async () => { if (budget-- !== 1) throw new Error('metadata unauthorized'); return { version, files: [{ url: artifactUrl, sha512: manifest.sha512, size: 42 }] }; }),
      downloadUpdate: vi.fn(async () => { if (budget-- !== 1) throw new Error('artifact unauthorized'); if (downloads++ === 0) throw new Error('offline'); return ['/private/cache/update.exe']; }),
    });
    const subject = controller(updater); await expect(subject.check()).resolves.toMatchObject({ state: 'available' });
    await expect(subject.updateNow()).resolves.toBe(false); expect(subject.status().state).toBe('failed');
    await expect(subject.updateNow()).resolves.toBe(true); expect(updater.downloadUpdate).toHaveBeenCalledTimes(2);
    expect(updater.authorize).toHaveBeenLastCalledWith(manifest);
  });

  it('publishes download progress and verifying/installing states', async () => {
    let progress!: (value: { percent: number; transferred: number; total: number }) => void;
    const updater = adapter({ onProgress: vi.fn((listener) => { progress = listener; }), downloadUpdate: vi.fn(async () => { progress({ percent: 50, transferred: 21, total: 42 }); return ['/private/cache/update.exe']; }) });
    const seen: string[] = []; const subject = new StableUpdateController({ currentVersion: '0.1.1', platform: 'win32', architecture: 'x64', fetch: vi.fn().mockResolvedValue(response(manifest)), updater, onStatus: (status) => seen.push(status.state) });
    await subject.check(); await subject.updateNow();
    expect(seen).toEqual(expect.arrayContaining(['downloading', 'verifying', 'installing']));
    expect(subject.status()).toMatchObject({ state: 'installing' });
  });
});

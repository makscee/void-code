import { describe, expect, it, vi } from 'vitest';
import { StableUpdateController, STABLE_MANIFEST_URL, type UpdaterAdapter } from '../src/main/stable-update';
const version = '0.2.0'; const feedUrl = 'https://vc.makscee.ru/download/windows/'; const artifactUrl = `${feedUrl}Void-Code-${version}-windows-x64.exe`; const sha512 = Buffer.alloc(64, 1).toString('base64');
const accepted = JSON.stringify({ schemaVersion: 1, status: 'accepted', version, platform: 'win32', architecture: 'x64', feedUrl, artifactUrl, immutableUrl: `https://github.com/makscee/void-code/releases/download/desktop-v${version}/Void-Code-${version}-windows-x64.exe`, sha256: 'b'.repeat(64), sha512, size: 1000, publisherName: 'Test Publisher', publishedAt: '2026-08-17T00:00:00.000Z' });
const response = (text: string) => ({ ok: true, body: (async function* () { yield new TextEncoder().encode(text); })() });
const updater = (): UpdaterAdapter => ({ configure: vi.fn(), authorize: vi.fn(), packageConfiguration: vi.fn().mockResolvedValue({ provider: 'generic', url: feedUrl, publisherNames: ['Test Publisher'] }), checkForUpdates: vi.fn().mockResolvedValue({ version, files: [{ url: artifactUrl, sha512, size: 1000 }] }), onProgress: vi.fn(), downloadUpdate: vi.fn(), sha256: vi.fn(), size: vi.fn(), remove: vi.fn(), cleanupPartials: vi.fn(), cleanupOwnedSessions: vi.fn(), quitAndInstall: vi.fn() });
describe('manual stable update retry', () => {
  it('coalesces a deferred startup/manual check and permits a later check', async () => {
    let resolveFetch!: (value: ReturnType<typeof response>) => void;
    const fetch = vi.fn()
      .mockImplementationOnce(() => new Promise((resolve) => { resolveFetch = resolve; }))
      .mockResolvedValueOnce(response(accepted));
    const generated = updater();
    const subject = new StableUpdateController({ currentVersion: '0.1.1', platform: 'win32', architecture: 'x64', fetch, updater: generated });

    const startup = subject.check();
    const manual = subject.check();
    expect(startup).toBe(manual);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(generated.checkForUpdates).not.toHaveBeenCalled();

    resolveFetch(response(accepted));
    const [startupResult, manualResult] = await Promise.all([startup, manual]);
    expect(startupResult).toBe(manualResult);
    expect(startupResult.state).toBe('available');
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(generated.checkForUpdates).toHaveBeenCalledTimes(1);

    await expect(subject.check()).resolves.toMatchObject({ state: 'available' });
    expect(fetch).toHaveBeenCalledTimes(2);
    expect(generated.checkForUpdates).toHaveBeenCalledTimes(2);
  });

  it('reports checking, survives failure, and replaces it on retry', async () => {
    const fetch = vi.fn().mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(response(accepted)); const observed: string[] = [];
    const subject = new StableUpdateController({ currentVersion: '0.1.1', platform: 'win32', architecture: 'x64', fetch, updater: updater(), onStatus: (status) => observed.push(status.state) });
    expect((await subject.check()).state).toBe('unavailable'); expect((await subject.check()).state).toBe('available');
    expect(fetch).toHaveBeenNthCalledWith(1, STABLE_MANIFEST_URL, expect.objectContaining({ redirect: 'error', signal: expect.any(AbortSignal) }));
    expect(observed).toEqual(['checking', 'unavailable', 'checking', 'available']);
  });
  it('bounds oversized responses and timeout', async () => {
    const oversized = new StableUpdateController({ currentVersion: '0.1.1', platform: 'win32', architecture: 'x64', fetch: vi.fn().mockResolvedValue(response('x'.repeat(65 * 1024))), updater: updater() });
    expect((await oversized.check()).state).toBe('unavailable');
    let aborted = false; const timed = new StableUpdateController({ currentVersion: '0.1.1', platform: 'win32', architecture: 'x64', updater: updater(), fetch: vi.fn((_url, options) => new Promise((_resolve, reject) => options.signal.addEventListener('abort', () => { aborted = true; reject(new Error('aborted')); }))), scheduleTimeout: (callback) => { queueMicrotask(callback); return 1; } });
    await timed.check(); expect(aborted).toBe(true);
  });
});

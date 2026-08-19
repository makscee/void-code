import { createHash } from 'node:crypto';
import { describe, expect, it, vi } from 'vitest';
import { BetaUpdateController, BETA_MANIFEST_URL, type BetaStatePersistence } from '../src/main/beta-update-controller';
import type { UpdaterAdapter } from '../src/main/stable-update';
import type { VerifiedEnvelope } from '../src/main/ed25519-beta';

const payload = {
  schema: 'vc-windows-update-v1', channel: 'closed-beta', keyId: 'test-beta-2026-01', version: '0.1.3-beta.2', platform: 'win32', architecture: 'x64', sequence: 2,
  installerUrl: 'https://vc.makscee.ru/download/windows/Void-Code-0.1.3-beta.2-windows-x64.exe',
  immutableUrl: 'https://github.com/makscee/void-code/releases/download/desktop-v0.1.3-beta.2/Void-Code-0.1.3-beta.2-windows-x64.exe',
  size: 42, sha256: 'a'.repeat(64), sha512: Buffer.alloc(64, 1).toString('base64'), publishedAt: '2026-08-17T00:00:00.000Z', notBefore: '2026-08-17T00:00:00.000Z', expiresAt: '2026-08-20T00:00:00.000Z',
};
const bytes = Buffer.from(JSON.stringify(payload));
const verified: VerifiedEnvelope = { payloadBytes: bytes, keyId: payload.keyId, digest: createHash('sha256').update(bytes).digest('hex') };
const response = { ok: true, body: (async function* () { yield Buffer.from('signed envelope'); })() };
function adapter(overrides: Partial<UpdaterAdapter> = {}): UpdaterAdapter {
  return {
    configure: vi.fn(), authorize: vi.fn(), packageConfiguration: vi.fn(), onProgress: vi.fn(),
    checkForUpdates: vi.fn().mockResolvedValue({ version: payload.version, files: [{ url: payload.installerUrl, sha512: payload.sha512, size: payload.size }] }),
    downloadUpdate: vi.fn().mockResolvedValue(['/private/cache/update.exe']), size: vi.fn().mockResolvedValue(42), sha256: vi.fn().mockResolvedValue(payload.sha256),
    remove: vi.fn().mockResolvedValue(undefined), cleanupPartials: vi.fn().mockResolvedValue(undefined), cleanupOwnedSessions: vi.fn().mockResolvedValue(undefined), quitAndInstall: vi.fn(), ...overrides,
  };
}
function stateStore(overrides: Partial<BetaStatePersistence> = {}): BetaStatePersistence { return { load: vi.fn().mockResolvedValue(undefined), save: vi.fn().mockResolvedValue(undefined), ...overrides }; }
function subject(updater = adapter(), store = stateStore(), overrides: Record<string, unknown> = {}) {
  return new BetaUpdateController({ currentVersion: '0.1.3-beta.1', platform: 'win32', architecture: 'x64', updater, stateStore: store,
    fetch: vi.fn().mockResolvedValue(response), verifyEnvelopeForTestOnlyPendingCeremony: vi.fn().mockReturnValue(verified), now: () => new Date('2026-08-18T00:00:00.000Z'), ...overrides });
}

describe('closed-beta controller chain', () => {
  it('persists accepted metadata only after download verification and owned-process cleanup, then launches', async () => {
    const events: string[] = []; const updater = adapter({
      cleanupOwnedSessions: vi.fn(async () => { events.push('cleanup'); }),
      size: vi.fn().mockResolvedValue(42), sha256: vi.fn().mockResolvedValue(payload.sha256),
      quitAndInstall: vi.fn(() => { events.push('launch'); }),
    });
    const store = stateStore({ save: vi.fn(async () => { events.push('save'); }) });
    const controller = subject(updater, store, { onStatus: (status: { state: string }) => { if (status.state === 'available') events.push('available'); } });
    await expect(controller.check()).resolves.toMatchObject({ state: 'available', availableVersion: payload.version });
    expect(events).toEqual(['available']);
    expect(updater.configure).toHaveBeenCalledWith(expect.objectContaining({ allowPrerelease: true, allowDowngrade: false }));
    expect(updater.packageConfiguration).not.toHaveBeenCalled();
    await expect(controller.updateNow()).resolves.toBe(true);
    expect(updater.downloadUpdate).toHaveBeenCalledWith(payload.size);
    expect(store.save).toHaveBeenCalledTimes(1);
    expect(store.save).toHaveBeenCalledWith(expect.objectContaining({ version: payload.version, sequence: 2, manifestDigest: verified.digest, keyId: payload.keyId }));
    expect(updater.size).toHaveBeenCalledTimes(2); expect(updater.sha256).toHaveBeenCalledTimes(2);
    expect(events).toEqual(['available', 'cleanup', 'save', 'launch']);
  });

  it('does not strand a client that only checked, while persisted installed state still rejects an older signed manifest', async () => {
    const accepted = { schema: 1 as const, channel: 'closed-beta' as const, version: payload.version, sequence: payload.sequence, manifestDigest: verified.digest, keyId: payload.keyId };
    const firstStore = stateStore(); const first = subject(adapter(), firstStore); await expect(first.check()).resolves.toMatchObject({ state: 'available' });
    expect(firstStore.save).not.toHaveBeenCalled();
    await expect(subject(adapter(), stateStore()).check()).resolves.toMatchObject({ state: 'available' });
    const olderBytes = Buffer.from(JSON.stringify({ ...payload, version: '0.1.3-beta.1', sequence: 1,
      installerUrl: 'https://vc.makscee.ru/download/windows/Void-Code-0.1.3-beta.1-windows-x64.exe',
      immutableUrl: 'https://github.com/makscee/void-code/releases/download/desktop-v0.1.3-beta.1/Void-Code-0.1.3-beta.1-windows-x64.exe' }));
    const older = { payloadBytes: olderBytes, keyId: payload.keyId, digest: createHash('sha256').update(olderBytes).digest('hex') };
    await expect(subject(adapter(), stateStore({ load: vi.fn().mockResolvedValue(accepted) }), { verifyEnvelopeForTestOnlyPendingCeremony: vi.fn().mockReturnValue(older) }).check()).resolves.toMatchObject({ state: 'unavailable' });
  });

  it('allows install at the inclusive signed validity-window boundaries', async () => {
    let now = new Date(payload.notBefore); const updater = adapter(); const controller = subject(updater, stateStore(), { now: () => now });
    await expect(controller.check()).resolves.toMatchObject({ state: 'available' });
    now = new Date(payload.expiresAt);
    await expect(controller.updateNow()).resolves.toBe(true);
    expect(updater.downloadUpdate).toHaveBeenCalledTimes(1);
  });

  it('fails closed without authorization or download when the manifest expires between check and install', async () => {
    let now = new Date('2026-08-18T00:00:00.000Z'); const updater = adapter(); const controller = subject(updater, stateStore(), { now: () => now });
    await expect(controller.check()).resolves.toMatchObject({ state: 'available' });
    const authorizations = vi.mocked(updater.authorize).mock.calls.length; now = new Date('2026-08-20T00:00:00.001Z');
    await expect(controller.updateNow()).resolves.toBe(false);
    expect(updater.authorize).toHaveBeenCalledTimes(authorizations); expect(updater.downloadUpdate).not.toHaveBeenCalled(); expect(updater.quitAndInstall).not.toHaveBeenCalled();
  });

  it('fails closed without authorization or download when the clock moves before notBefore', async () => {
    let now = new Date('2026-08-18T00:00:00.000Z'); const updater = adapter(); const controller = subject(updater, stateStore(), { now: () => now });
    await expect(controller.check()).resolves.toMatchObject({ state: 'available' });
    const authorizations = vi.mocked(updater.authorize).mock.calls.length; now = new Date('2026-08-16T23:59:59.999Z');
    await expect(controller.updateNow()).resolves.toBe(false);
    expect(updater.authorize).toHaveBeenCalledTimes(authorizations); expect(updater.downloadUpdate).not.toHaveBeenCalled(); expect(updater.quitAndInstall).not.toHaveBeenCalled();
  });

  it('revalidates expiry after every asynchronous verification immediately before launch', async () => {
    let now = new Date('2026-08-18T00:00:00.000Z'); let hashCalls = 0;
    const updater = adapter({ sha256: vi.fn(async () => { if (++hashCalls === 2) now = new Date('2026-08-20T00:00:00.001Z'); return payload.sha256; }) });
    const controller = subject(updater, stateStore(), { now: () => now });
    await expect(controller.check()).resolves.toMatchObject({ state: 'available' });
    await expect(controller.updateNow()).resolves.toBe(false);
    expect(updater.downloadUpdate).toHaveBeenCalledTimes(1); expect(updater.remove).toHaveBeenCalled(); expect(updater.quitAndInstall).not.toHaveBeenCalled();
  });

  it('reauthorizes the verified manifest for each direct retry after a failed download consumes its budget', async () => {
    let budget = 0; let downloads = 0;
    const updater = adapter({
      authorize: vi.fn((authorized) => { budget = authorized ? 1 : 0; }),
      checkForUpdates: vi.fn(async () => { if (budget-- !== 1) throw new Error('metadata unauthorized'); return { version: payload.version, files: [{ url: payload.installerUrl, sha512: payload.sha512, size: payload.size }] }; }),
      downloadUpdate: vi.fn(async () => { if (budget-- !== 1) throw new Error('artifact unauthorized'); if (downloads++ === 0) throw new Error('offline'); return ['/private/cache/update.exe']; }),
    });
    const controller = subject(updater); await expect(controller.check()).resolves.toMatchObject({ state: 'available' });
    await expect(controller.updateNow()).resolves.toBe(false); await expect(controller.updateNow()).resolves.toBe(true);
    expect(updater.downloadUpdate).toHaveBeenCalledTimes(2);
    expect(updater.authorize).toHaveBeenLastCalledWith({ artifactUrl: payload.installerUrl, immutableUrl: payload.immutableUrl, size: payload.size });
  });

  it('fails closed before launch when accepted-state persistence fails', async () => {
    const statuses: string[] = []; const updater = adapter();
    const controller = subject(updater, stateStore({ save: vi.fn().mockRejectedValue(new Error('disk')) }), { onStatus: (status: { state: string }) => statuses.push(status.state) });
    await expect(controller.check()).resolves.toMatchObject({ state: 'available' });
    await expect(controller.updateNow()).resolves.toBe(false);
    expect(statuses).not.toContain('installing'); expect(updater.downloadUpdate).toHaveBeenCalledTimes(1); expect(updater.quitAndInstall).not.toHaveBeenCalled();
  });

  it('requests only the fixed beta endpoint with redirects disabled', async () => {
    const fetch = vi.fn().mockResolvedValue(response); await subject(adapter(), stateStore(), { fetch }).check();
    expect(fetch).toHaveBeenCalledWith(BETA_MANIFEST_URL, expect.objectContaining({ redirect: 'error' }));
  });

  it.each([
    ['signature/ceremony', { verifyEnvelopeForTestOnlyPendingCeremony: undefined }],
    ['corrupt persisted state', { stateStore: stateStore({ load: vi.fn().mockRejectedValue(new Error('corrupt')) }) }],
    ['metadata URL', { updater: adapter({ checkForUpdates: vi.fn().mockResolvedValue({ version: payload.version, files: [{ url: 'https://evil.example/x', sha512: payload.sha512, size: 42 }] }) }) }],
  ])('fails closed before download for %s', async (_name, change) => {
    const controller = subject(adapter(), stateStore(), change); expect((await controller.check()).state).toBe('unavailable');
    await expect(controller.updateNow()).resolves.toBe(false);
  });

  it.each([
    ['download size', adapter({ size: vi.fn().mockResolvedValue(43) })],
    ['download hash', adapter({ sha256: vi.fn().mockResolvedValue('b'.repeat(64)) })],
    ['cleanup', adapter({ cleanupOwnedSessions: vi.fn().mockRejectedValue(new Error('busy child')) })],
    ['post-cleanup pathname change', adapter({ size: vi.fn().mockResolvedValueOnce(42).mockResolvedValueOnce(43) })],
  ])('never launches and cleans artifacts after %s failure', async (_name, updater) => {
    const controller = subject(updater); await controller.check(); await expect(controller.updateNow()).resolves.toBe(false);
    expect(updater.quitAndInstall).not.toHaveBeenCalled(); expect(updater.remove).toHaveBeenCalledWith('/private/cache/update.exe'); expect(updater.cleanupPartials).toHaveBeenCalled();
  });
});

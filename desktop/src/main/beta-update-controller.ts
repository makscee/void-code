import type { StableUpdateStatus } from '../shared/contract';
import { BETA_MANIFEST_MAX_BYTES, evaluateBetaPayload, verifyEd25519Envelope, type AcceptedBetaManifest, type BetaDefensiveState, type VerifiedEnvelope } from './ed25519-beta';
import type { UpdaterAdapter } from './stable-update';
import { productionBetaKey } from './update-trust';

export const BETA_MANIFEST_URL = 'https://vc.makscee.ru/desktop/closed-beta-v1.json';
const CHECK_TIMEOUT_MS = 10_000;
type FetchResponse = { ok: boolean; body: AsyncIterable<Uint8Array> | null };
type TimeoutHandle = ReturnType<typeof setTimeout> | number;
export interface BetaStatePersistence { load(): Promise<BetaDefensiveState | undefined>; save(state: BetaDefensiveState): Promise<void> }
export interface BetaUpdateControllerOptions {
  currentVersion: string; platform: NodeJS.Platform; architecture: string; updater?: UpdaterAdapter; stateStore: BetaStatePersistence;
  fetch: (url: string, options: { signal: AbortSignal; redirect: 'error' }) => Promise<FetchResponse>;
  onStatus?: (status: StableUpdateStatus) => void; now?: () => Date;
  scheduleTimeout?: (callback: () => void, milliseconds: number) => TimeoutHandle; clearScheduledTimeout?: (handle: TimeoutHandle) => void;
  /** TEST ONLY while the production allowlist is intentionally empty pending ceremony. */
  verifyEnvelopeForTestOnlyPendingCeremony?: (bytes: Uint8Array) => VerifiedEnvelope;
}
export class BetaUpdateController {
  private currentStatus: StableUpdateStatus; private manifest?: AcceptedBetaManifest; private busy = false; private checkInFlight?: Promise<StableUpdateStatus>;
  private readonly scheduleTimeout; private readonly clearScheduledTimeout;
  constructor(private readonly options: BetaUpdateControllerOptions) {
    this.currentStatus = { state: 'unavailable', currentVersion: options.currentVersion, canRetry: true };
    this.scheduleTimeout = options.scheduleTimeout ?? ((callback, milliseconds) => setTimeout(callback, milliseconds));
    this.clearScheduledTimeout = options.clearScheduledTimeout ?? ((handle) => clearTimeout(handle));
    options.updater?.configure({ autoDownload: false, autoInstallOnAppQuit: false, allowPrerelease: true, allowDowngrade: false, disableWebInstaller: true, disableDifferentialDownload: true });
    options.updater?.onProgress((progress) => {
      if (!this.manifest) return; const total = this.manifest.size;
      const transferred = Number.isFinite(progress.transferred) && progress.transferred >= 0 ? Math.min(progress.transferred, total) : 0;
      this.publish({ state: 'downloading', currentVersion: options.currentVersion, availableVersion: this.manifest.version, percent: Math.min(100, transferred / total * 100), transferred, total, canRetry: false });
    });
  }
  status(): StableUpdateStatus { return this.currentStatus; }
  private publish(status: StableUpdateStatus): StableUpdateStatus { this.currentStatus = status; this.options.onStatus?.(status); return status; }
  check(): Promise<StableUpdateStatus> {
    if (this.busy) return Promise.resolve(this.currentStatus); if (this.checkInFlight) return this.checkInFlight;
    const inFlight = this.performCheck().finally(() => { if (this.checkInFlight === inFlight) this.checkInFlight = undefined; }); this.checkInFlight = inFlight; return inFlight;
  }
  private async performCheck(): Promise<StableUpdateStatus> {
    this.publish({ state: 'checking', currentVersion: this.options.currentVersion, canRetry: false }); this.options.updater?.authorize();
    if (this.options.platform !== 'win32' || this.options.architecture !== 'x64' || !this.options.updater) return this.publish({ state: 'unavailable', currentVersion: this.options.currentVersion, canRetry: true });
    const abort = new AbortController(); const timer = this.scheduleTimeout(() => abort.abort(), CHECK_TIMEOUT_MS);
    try {
      const response = await this.options.fetch(BETA_MANIFEST_URL, { signal: abort.signal, redirect: 'error' }); if (!response.ok || !response.body) throw new Error('manifest request failed');
      const chunks: Buffer[] = []; let size = 0; for await (const chunk of response.body) { size += chunk.byteLength; if (size > BETA_MANIFEST_MAX_BYTES) { abort.abort(); throw new Error('manifest too large'); } chunks.push(Buffer.from(chunk)); }
      const envelopeBytes = Buffer.concat(chunks);
      // Signature verification is deliberately the first interpretation of payload bytes.
      const verified = this.options.verifyEnvelopeForTestOnlyPendingCeremony?.(envelopeBytes) ?? verifyEd25519Envelope(envelopeBytes, productionBetaKey);
      const state = await this.options.stateStore.load();
      const manifest = evaluateBetaPayload(verified, { currentVersion: this.options.currentVersion, platform: this.options.platform, architecture: this.options.architecture, now: this.options.now?.() ?? new Date() }, state);
      if (manifest.version === this.options.currentVersion) {
        this.manifest = undefined;
        return this.publish({ state: 'up-to-date', currentVersion: this.options.currentVersion, canRetry: false });
      }
      this.options.updater.authorize({ artifactUrl: manifest.installerUrl, immutableUrl: manifest.immutableUrl, size: manifest.size });
      const generated = await this.options.updater.checkForUpdates(); if (generated.version !== manifest.version || generated.files.length !== 1) throw new Error('generated metadata mismatch');
      const file = generated.files[0]; if (file.url !== manifest.installerUrl || file.sha512 !== manifest.sha512 || file.size !== manifest.size) throw new Error('generated artifact mismatch');
      this.manifest = manifest; return this.publish({ state: 'available', currentVersion: this.options.currentVersion, availableVersion: manifest.version, canRetry: false });
    } catch { this.manifest = undefined; this.options.updater.authorize(); return this.publish({ state: 'unavailable', currentVersion: this.options.currentVersion, canRetry: true }); } finally { this.clearScheduledTimeout(timer); }
  }
  async updateNow(): Promise<boolean> {
    if (this.busy || (this.currentStatus.state !== 'available' && this.currentStatus.state !== 'failed') || !this.manifest || !this.options.updater) return false;
    this.busy = true; const updater = this.options.updater; const manifest = this.manifest; let artifact: string | undefined;
    try {
      const now = this.options.now?.() ?? new Date();
      if (now < new Date(manifest.notBefore) || now > new Date(manifest.expiresAt)) throw new Error('manifest validity window no longer permits install');
      updater.authorize({ artifactUrl: manifest.installerUrl, immutableUrl: manifest.immutableUrl, size: manifest.size });
      this.publish({ state: 'downloading', currentVersion: this.options.currentVersion, availableVersion: manifest.version, percent: 0, transferred: 0, total: manifest.size, canRetry: false });
      const files = await updater.downloadUpdate(manifest.size); if (files.length !== 1) throw new Error('unexpected downloaded files'); artifact = files[0];
      this.publish({ state: 'verifying', currentVersion: this.options.currentVersion, availableVersion: manifest.version, canRetry: false });
      if (await updater.size(artifact) !== manifest.size || await updater.sha256(artifact) !== manifest.sha256) throw new Error('independent verification failed');
      await updater.cleanupOwnedSessions();
      if (await updater.size(artifact) !== manifest.size || await updater.sha256(artifact) !== manifest.sha256) throw new Error('artifact changed before launch');
      const launchTime = this.options.now?.() ?? new Date();
      if (launchTime < new Date(manifest.notBefore) || launchTime > new Date(manifest.expiresAt)) throw new Error('manifest validity window elapsed before launch');
      await this.options.stateStore.save({ schema: 1, channel: 'closed-beta', version: manifest.version, sequence: manifest.sequence, manifestDigest: manifest.manifestDigest, keyId: manifest.keyId });
      this.publish({ state: 'installing', currentVersion: this.options.currentVersion, availableVersion: manifest.version, canRetry: false }); updater.quitAndInstall(true, true); return true;
    } catch { if (artifact) await updater.remove(artifact).catch(() => undefined); await updater.cleanupPartials().catch(() => undefined); this.publish({ state: 'failed', currentVersion: this.options.currentVersion, availableVersion: manifest.version, canRetry: true }); return false; } finally { this.busy = false; }
  }
}

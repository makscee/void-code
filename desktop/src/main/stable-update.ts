import { createHash } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { readFile, rm, stat } from 'node:fs/promises';
import path from 'node:path';
import { load as loadYaml } from 'js-yaml';
import type { StableUpdateStatus } from '../shared/contract';

export const STABLE_MANIFEST_URL = 'https://vc.makscee.ru/desktop/stable-v1.json';
export const UPDATE_FEED_URL = 'https://vc.makscee.ru/download/windows/';
export const UPDATE_CHECK_TIMEOUT_MS = 10_000;
export const UPDATE_MANIFEST_MAX_BYTES = 64 * 1024;

const STABLE_SEMVER = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;
const SHA512_BASE64 = /^(?:[A-Za-z0-9+/]{4}){21}[A-Za-z0-9+/]{2}==$/;
const ACCEPTED_KEYS = ['architecture', 'artifactUrl', 'feedUrl', 'immutableUrl', 'platform', 'publishedAt', 'publisherName', 'schemaVersion', 'sha256', 'sha512', 'size', 'status', 'version'];
type StableVersion = [bigint, bigint, bigint];
export interface AcceptedUpdateManifest { schemaVersion: 1; status: 'accepted'; version: string; platform: 'win32'; architecture: 'x64'; feedUrl: string; artifactUrl: string; immutableUrl: string; sha256: string; sha512: string; size: number; publisherName: string; publishedAt: string }
export interface GeneratedUpdateInfo { version: string; files: Array<{ url: string; sha512: string; size: number }> }
export interface AuthorizedUpdateUrls { artifactUrl: string; immutableUrl: string; size: number }
export interface UpdaterAdapter {
  configure(settings: { autoDownload: false; autoInstallOnAppQuit: false; allowPrerelease: boolean; allowDowngrade: false; disableWebInstaller: true; disableDifferentialDownload: true }): void;
  authorize(manifest?: AuthorizedUpdateUrls): void;
  packageConfiguration(): Promise<{ provider: 'generic'; url: string; publisherNames: string[] }>;
  checkForUpdates(): Promise<GeneratedUpdateInfo>;
  onProgress(listener: (progress: { percent: number; transferred: number; total: number }) => void): void;
  downloadUpdate(maxBytes: number): Promise<string[]>;
  sha256(file: string): Promise<string>; size(file: string): Promise<number>; remove(file: string): Promise<void>;
  cleanupPartials(): Promise<void>; cleanupOwnedSessions(): Promise<void>; quitAndInstall(isSilent: boolean, isForceRunAfter: boolean): void;
}

function exactKeys(value: Record<string, unknown>, expected: string[]): boolean { const keys = Object.keys(value).sort(); const wanted = [...expected].sort(); return keys.length === wanted.length && keys.every((key, index) => key === wanted[index]); }
function stableVersion(value: unknown): StableVersion | undefined { if (typeof value !== 'string') return; const match = STABLE_SEMVER.exec(value); return match ? [BigInt(match[1]), BigInt(match[2]), BigInt(match[3])] : undefined; }
function newer(candidate: StableVersion, current: StableVersion): boolean { for (let index = 0; index < 3; index++) { if (candidate[index] > current[index]) return true; if (candidate[index] < current[index]) return false; } return false; }
function canonicalDate(value: unknown): boolean { if (typeof value !== 'string') return false; const date = new Date(value); return !Number.isNaN(date.valueOf()) && date.toISOString() === value; }
function acceptedManifest(raw: unknown): AcceptedUpdateManifest | undefined {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) return;
  const value = raw as Record<string, unknown>;
  if (!exactKeys(value, ACCEPTED_KEYS) || value.schemaVersion !== 1 || value.status !== 'accepted' || value.platform !== 'win32' || value.architecture !== 'x64') return;
  const version = stableVersion(value.version); if (!version) return;
  const artifact = `https://vc.makscee.ru/download/windows/Void-Code-${value.version}-windows-x64.exe`;
  const immutable = `https://github.com/makscee/void-code/releases/download/desktop-v${value.version}/Void-Code-${value.version}-windows-x64.exe`;
  if (value.feedUrl !== UPDATE_FEED_URL || value.artifactUrl !== artifact || value.immutableUrl !== immutable) return;
  if (typeof value.sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.sha256) || typeof value.sha512 !== 'string' || !SHA512_BASE64.test(value.sha512)) return;
  if (!Number.isSafeInteger(value.size) || Number(value.size) <= 0 || typeof value.publisherName !== 'string' || value.publisherName.trim() !== value.publisherName || value.publisherName.length === 0 || !canonicalDate(value.publishedAt)) return;
  return value as unknown as AcceptedUpdateManifest;
}
export function evaluateStableManifest(raw: unknown, currentVersion: string, platform: NodeJS.Platform, architecture: string): StableUpdateStatus {
  const unavailable = (): StableUpdateStatus => ({ state: 'unavailable', currentVersion, canRetry: true });
  if (platform !== 'win32' || architecture !== 'x64') return unavailable();
  if (typeof raw === 'object' && raw !== null && !Array.isArray(raw) && exactKeys(raw as Record<string, unknown>, ['schemaVersion', 'status']) && (raw as Record<string, unknown>).schemaVersion === 1 && (raw as Record<string, unknown>).status === 'unavailable') return unavailable();
  const manifest = acceptedManifest(raw); const candidate = stableVersion(manifest?.version); const current = stableVersion(currentVersion);
  if (!manifest || !candidate || !current) return unavailable();
  return newer(candidate, current) ? { state: 'available', currentVersion, availableVersion: manifest.version, canRetry: false } : { state: 'up-to-date', currentVersion, canRetry: false };
}

function secureUrl(raw: string): URL | undefined {
  try { const url = new URL(raw); return url.protocol === 'https:' && !url.username && !url.password && !url.port && !url.hash ? url : undefined; } catch { return undefined; }
}
function validNoCache(url: URL): boolean {
  if (url.pathname !== '/download/windows/latest.yml' || url.searchParams.size !== 1) return false;
  const value = url.searchParams.get('noCache');
  if (!value || !/^[1-9a-v][0-9a-v]{8}$/.test(value)) return false;
  const timestamp = Number.parseInt(value, 32);
  return Number.isSafeInteger(timestamp) && timestamp >= Date.UTC(2020, 0, 1) && timestamp <= Date.now() + 5 * 60_000 && timestamp.toString(32) === value;
}
function validReleaseAsset(url: URL): boolean {
  return url.hostname === 'release-assets.githubusercontent.com' && url.pathname.startsWith('/github-production-release-asset/') && url.pathname.length > '/github-production-release-asset/'.length;
}

export class UpdaterRequestAuthorizer {
  private manifest?: AuthorizedUpdateUrls;
  private metadataAvailable = true;
  private artifactAvailable = true;
  private artifactRequested = false;
  private immutableApproved = false;
  private immutableRequested = false;
  private releaseAssetApproved?: string;
  private readonly responses = new Map<string, 'metadata' | 'artifact'>();

  authorize(manifest?: AuthorizedUpdateUrls): void {
    this.manifest = manifest; this.metadataAvailable = true; this.artifactAvailable = true; this.artifactRequested = false; this.immutableApproved = false; this.immutableRequested = false; this.releaseAssetApproved = undefined; this.responses.clear();
  }
  allowRequest(raw: string): boolean {
    const manifest = this.manifest; const url = secureUrl(raw); if (!manifest || !url) return false;
    if (this.metadataAvailable && url.hostname === 'vc.makscee.ru' && validNoCache(url)) { this.metadataAvailable = false; this.responses.set(raw, 'metadata'); return true; }
    if (this.artifactAvailable && raw === manifest.artifactUrl) { this.artifactAvailable = false; this.artifactRequested = true; this.responses.set(raw, 'artifact'); return true; }
    if (raw === manifest.immutableUrl && this.immutableApproved) { this.immutableApproved = false; this.immutableRequested = true; this.responses.set(raw, 'artifact'); return true; }
    if (raw === this.releaseAssetApproved) { this.releaseAssetApproved = undefined; this.responses.set(raw, 'artifact'); return true; }
    return false;
  }
  consumeResponseAuthorization(raw: string): { kind: 'metadata' } | { kind: 'artifact'; size: number } | undefined {
    const kind = this.responses.get(raw); this.responses.delete(raw);
    if (kind === 'metadata') return { kind };
    if (kind === 'artifact' && this.manifest) return { kind, size: this.manifest.size };
    return undefined;
  }
  observeRedirect(source: string, target: string): void {
    const manifest = this.manifest; if (!manifest) return;
    if (this.artifactRequested && source === manifest.artifactUrl && target === manifest.immutableUrl) { this.artifactRequested = false; this.immutableApproved = true; return; }
    const targetUrl = secureUrl(target);
    if (this.immutableRequested && source === manifest.immutableUrl && targetUrl && validReleaseAsset(targetUrl)) { this.immutableRequested = false; this.releaseAssetApproved = target; }
  }
}

type FetchResponse = { ok: boolean; body: AsyncIterable<Uint8Array> | null };
type TimeoutHandle = ReturnType<typeof setTimeout> | number;
export interface StableUpdateControllerOptions { currentVersion: string; platform: NodeJS.Platform; architecture: string; fetch: (url: string, options: { signal: AbortSignal; redirect: 'error' }) => Promise<FetchResponse>; updater?: UpdaterAdapter; onStatus?: (status: StableUpdateStatus) => void; scheduleTimeout?: (callback: () => void, milliseconds: number) => TimeoutHandle; clearScheduledTimeout?: (handle: TimeoutHandle) => void }
export class StableUpdateController {
  private currentStatus: StableUpdateStatus; private manifest?: AcceptedUpdateManifest; private busy = false; private checkInFlight?: Promise<StableUpdateStatus>;
  private readonly scheduleTimeout; private readonly clearScheduledTimeout;
  constructor(private readonly options: StableUpdateControllerOptions) {
    this.currentStatus = { state: 'unavailable', currentVersion: options.currentVersion, canRetry: true };
    this.scheduleTimeout = options.scheduleTimeout ?? ((callback: () => void, milliseconds: number) => setTimeout(callback, milliseconds));
    this.clearScheduledTimeout = options.clearScheduledTimeout ?? ((handle: TimeoutHandle) => clearTimeout(handle));
    options.updater?.configure({ autoDownload: false, autoInstallOnAppQuit: false, allowPrerelease: false, allowDowngrade: false, disableWebInstaller: true, disableDifferentialDownload: true });
    options.updater?.onProgress((progress) => {
      if (!this.manifest) return;
      const total = Number.isFinite(progress.total) && progress.total > 0 ? progress.total : this.manifest.size;
      const transferred = Number.isFinite(progress.transferred) && progress.transferred >= 0 ? Math.min(progress.transferred, total) : 0;
      const percent = Number.isFinite(progress.percent) ? Math.min(100, Math.max(0, progress.percent)) : Math.min(100, transferred / total * 100);
      this.publish({ state: 'downloading', currentVersion: options.currentVersion, availableVersion: this.manifest.version, percent, transferred, total, canRetry: false });
    });
  }
  status(): StableUpdateStatus { return this.currentStatus; }
  private publish(status: StableUpdateStatus): StableUpdateStatus { this.currentStatus = status; this.options.onStatus?.(status); return status; }
  check(): Promise<StableUpdateStatus> {
    if (this.busy) return Promise.resolve(this.currentStatus);
    if (this.checkInFlight) return this.checkInFlight;
    const inFlight = this.performCheck().finally(() => { if (this.checkInFlight === inFlight) this.checkInFlight = undefined; });
    this.checkInFlight = inFlight;
    return inFlight;
  }
  private async performCheck(): Promise<StableUpdateStatus> {
    this.publish({ state: 'checking', currentVersion: this.options.currentVersion, canRetry: false });
    this.options.updater?.authorize();
    if (this.options.platform !== 'win32' || this.options.architecture !== 'x64' || !this.options.updater) return this.publish({ state: 'unavailable', currentVersion: this.options.currentVersion, canRetry: true });
    const abort = new AbortController(); const timer = this.scheduleTimeout(() => abort.abort(), UPDATE_CHECK_TIMEOUT_MS);
    try {
      const response = await this.options.fetch(STABLE_MANIFEST_URL, { signal: abort.signal, redirect: 'error' }); if (!response.ok || !response.body) throw new Error('manifest request failed');
      const chunks: Uint8Array[] = []; let bytes = 0; for await (const chunk of response.body) { bytes += chunk.byteLength; if (bytes > UPDATE_MANIFEST_MAX_BYTES) { abort.abort(); throw new Error('manifest too large'); } chunks.push(chunk); }
      const raw = JSON.parse(Buffer.concat(chunks.map((chunk) => Buffer.from(chunk))).toString('utf8')) as unknown;
      const evaluated = evaluateStableManifest(raw, this.options.currentVersion, this.options.platform, this.options.architecture); if (evaluated.state !== 'available') { this.manifest = undefined; return this.publish(evaluated); }
      const manifest = acceptedManifest(raw)!; const packaged = await this.options.updater.packageConfiguration();
      if (packaged.provider !== 'generic' || packaged.url !== UPDATE_FEED_URL || packaged.publisherNames.length !== 1 || packaged.publisherNames[0] !== manifest.publisherName) throw new Error('publisher/feed unavailable');
      this.options.updater.authorize(manifest);
      const generated = await this.options.updater.checkForUpdates();
      if (generated.version !== manifest.version || generated.files.length !== 1) throw new Error('generated metadata mismatch'); const file = generated.files[0];
      if (file.url !== manifest.artifactUrl || file.sha512 !== manifest.sha512 || file.size !== manifest.size) throw new Error('generated artifact mismatch');
      this.manifest = manifest; return this.publish(evaluated);
    } catch { this.manifest = undefined; this.options.updater?.authorize(); return this.publish({ state: 'unavailable', currentVersion: this.options.currentVersion, canRetry: true }); } finally { this.clearScheduledTimeout(timer); }
  }
  async updateNow(): Promise<boolean> {
    if (this.busy || (this.currentStatus.state !== 'available' && this.currentStatus.state !== 'failed') || !this.manifest || !this.options.updater) return false;
    this.busy = true; const updater = this.options.updater; const manifest = this.manifest; let artifact: string | undefined;
    try {
      updater.authorize(manifest);
      this.publish({ state: 'downloading', currentVersion: this.options.currentVersion, availableVersion: manifest.version, percent: 0, transferred: 0, total: manifest.size, canRetry: false });
      const files = await updater.downloadUpdate(manifest.size); if (files.length !== 1) throw new Error('unexpected downloaded files'); artifact = files[0];
      this.publish({ state: 'verifying', currentVersion: this.options.currentVersion, availableVersion: manifest.version, canRetry: false });
      if (await updater.size(artifact) !== manifest.size || await updater.sha256(artifact) !== manifest.sha256) { await updater.remove(artifact); artifact = undefined; throw new Error('independent verification failed'); }
      await updater.cleanupOwnedSessions(); this.publish({ state: 'installing', currentVersion: this.options.currentVersion, availableVersion: manifest.version, canRetry: false }); updater.quitAndInstall(true, true); return true;
    } catch { if (artifact) await updater.remove(artifact).catch(() => undefined); await updater.cleanupPartials().catch(() => undefined); this.publish({ state: 'failed', currentVersion: this.options.currentVersion, availableVersion: manifest.version, canRetry: true }); return false; } finally { this.busy = false; }
  }
}

export async function fileSha256(file: string): Promise<string> { const hash = createHash('sha256'); await new Promise<void>((resolve, reject) => createReadStream(file).on('data', (chunk) => hash.update(chunk)).on('error', reject).on('end', resolve)); return hash.digest('hex'); }
export async function fileSize(file: string): Promise<number> { return (await stat(file)).size; }
export async function removeUpdateFile(file: string): Promise<void> { await rm(file, { force: true }); }
export function parseAppUpdateConfiguration(text: string): { provider: 'generic'; url: string; publisherNames: string[] } {
  const parsed = loadYaml(text, { schema: undefined });
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) throw new Error('app-update.yml must be an object');
  const value = parsed as Record<string, unknown>;
  if (value.provider !== 'generic' || value.url !== UPDATE_FEED_URL) throw new Error('app-update.yml provider/feed mismatch');
  const rawPublishers = typeof value.publisherName === 'string' ? [value.publisherName] : value.publisherName;
  if (!Array.isArray(rawPublishers) || rawPublishers.length !== 1 || typeof rawPublishers[0] !== 'string' || rawPublishers[0].trim() !== rawPublishers[0] || rawPublishers[0].length === 0) throw new Error('app-update.yml requires one publisher');
  return { provider: 'generic', url: UPDATE_FEED_URL, publisherNames: [rawPublishers[0]] };
}
export async function readAppUpdateConfiguration(resourcesPath: string): Promise<{ provider: 'generic'; url: string; publisherNames: string[] }> {
  return parseAppUpdateConfiguration(await readFile(path.join(resourcesPath, 'app-update.yml'), 'utf8'));
}

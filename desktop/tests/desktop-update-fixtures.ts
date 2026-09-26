import { createHash, generateKeyPairSync, sign } from 'node:crypto';

export type Artifact = { platform: string; arch: string; file: string; size: number; sha256: string };
export type Manifest = { schema: 1; product: string; channel: string; version: string; tag: string; artifacts: Artifact[] };
export type InstalledBuild = { product: string; channel: string; version: string; platform: string; arch: string; packaged: boolean };
export type VerifiedPayload = { payloadBytes: Uint8Array; payloadDigest: string; keyId: string };
export type VerifyResult = { ok: true; verified: VerifiedPayload } | { ok: false; code: string; verified?: never };
export type SelectionResult = { ok: true; plan: UpdatePlan } | { ok: false; code: string };
export type TrustModule = {
  verifyDesktopUpdateEnvelope(raw: Uint8Array, keyring: Readonly<Record<string, string | Uint8Array>>): VerifyResult;
  parseAndSelectDesktopRelease(payloadBytes: Uint8Array, installed: InstalledBuild): SelectionResult;
};
export type ReplayFloor = { version: string; payloadDigest: string };
export type UpdateSnapshot = { state: 'idle' | 'checking' | 'up-to-date' | 'available' | 'unavailable' | 'unsupported' | 'downloading' | 'verifying' | 'ready' | 'confirming' | 'preparing' | 'installing' | 'failed'; version?: string; progress?: number; error?: string };
export type UpdatePlan = { version: string; payloadDigest: string; artifact: Artifact; payloadBytes: Uint8Array };
/** Opaque location of bytes staged outside the controller heap. */
export type StageHandle = { readonly id: string };
export type ControllerDependencies = {
  installed: InstalledBuild;
  keyring: Readonly<Record<string, string | Uint8Array>>;
  loadFloor(): Promise<ReplayFloor | undefined>;
  saveFloor(floor: ReplayFloor): Promise<void>;
  fetchMetadata(signal: AbortSignal): Promise<Uint8Array>;
  download(plan: UpdatePlan, signal: AbortSignal, progress: (fraction: number) => void): Promise<StageHandle>;
  verifyArtifact(plan: UpdatePlan, stage: StageHandle): Promise<boolean>;
  showNativeInstallDialog(plan: UpdatePlan): Promise<boolean>;
  prepare(plan: UpdatePlan, stage: StageHandle): Promise<void>;
  reverify(plan: UpdatePlan, stage: StageHandle): Promise<boolean>;
  handoff(plan: UpdatePlan, stage: StageHandle): Promise<void>;
  onSnapshot(snapshot: UpdateSnapshot): void;
  metadataTimeoutMs?: number;
};
export type DesktopUpdateController = { check(options?: { manual?: boolean }): Promise<void>; download(): Promise<void>; cancelDownload(): void; requestInstall(): Promise<void>; dispose(): void; snapshot(): UpdateSnapshot };
export type ControllerModule = { createDesktopUpdateController(dependencies: ControllerDependencies): DesktopUpdateController };

const pair = generateKeyPairSync('ed25519');
export const testKeyId = 'test-ed25519-2026';
export const testPublicKey = pair.publicKey.export({ type: 'spki', format: 'pem' }).toString();
const privateKey = pair.privateKey;
export const keyring = Object.freeze({ [testKeyId]: testPublicKey });
export const encoder = new TextEncoder();
export const decoder = new TextDecoder('utf-8', { fatal: true });
export const sha256 = (bytes: Uint8Array): string => createHash('sha256').update(bytes).digest('hex');
export const artifactBytes = (): Uint8Array => new Uint8Array([1, 2, 3, 4]);
export function artifactFor(bytes = artifactBytes(), overrides: Partial<Artifact> = {}): Artifact {
  return { platform: 'win32', arch: 'x64', file: 'Void-Code-windows-x64.exe', size: bytes.byteLength, sha256: sha256(bytes), ...overrides };
}
export const defaultArtifact = (): Artifact => artifactFor();
export function manifest(overrides: Partial<Manifest> = {}): Manifest {
  const version = overrides.version ?? '1.2.3';
  return { schema: 1, product: 'works.voidcode.desktop', channel: 'stable', version, tag: `v${version}`, artifacts: [defaultArtifact()], ...overrides };
}
export function payloadBytes(value: Manifest = manifest()): Uint8Array {
  return encoder.encode(JSON.stringify(value));
}
export function envelope(payload = payloadBytes(), options: { keyId?: string; signature?: Uint8Array; schema?: number; extra?: Record<string, unknown> } = {}): Uint8Array {
  const signature = options.signature ?? sign(null, payload, privateKey);
  return encoder.encode(JSON.stringify({ schema: options.schema ?? 1, keyId: options.keyId ?? testKeyId, payload: Buffer.from(payload).toString('base64'), signature: Buffer.from(signature).toString('base64'), ...options.extra }));
}
/** Changes one envelope string field while preserving the original signature and all decoded bytes. */
export function replaceEnvelopeField(raw: Uint8Array, field: 'payload' | 'signature', value: string): Uint8Array {
  const parsed = JSON.parse(decoder.decode(raw)) as Record<string, unknown>;
  parsed[field] = value;
  return encoder.encode(JSON.stringify(parsed));
}
export function signedRawPayload(text: string): Uint8Array {
  return envelope(encoder.encode(text));
}
export const installed = (overrides: Partial<InstalledBuild> = {}): InstalledBuild => ({ product: 'works.voidcode.desktop', channel: 'stable', version: '1.0.0', platform: 'win32', arch: 'x64', packaged: true, ...overrides });
export function deferred<T>(): { promise: Promise<T>; resolve(value: T): void; reject(reason?: unknown): void } {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}
export async function ticks(): Promise<void> {
  await Promise.resolve();
  await Promise.resolve();
}

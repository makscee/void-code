import { createHash, createPublicKey, verify } from 'node:crypto';

export const BETA_MANIFEST_MAX_BYTES = 64 * 1024;
export const BETA_INSTALLER_MAX_BYTES = 2 * 1024 * 1024 * 1024;
const ENVELOPE_KEYS = ['keyId', 'payload', 'schema', 'signature'];
const PAYLOAD_KEYS = ['architecture', 'channel', 'expiresAt', 'immutableUrl', 'installerUrl', 'keyId', 'notBefore', 'platform', 'publishedAt', 'schema', 'sequence', 'sha256', 'sha512', 'size', 'version'];
const BETA_VERSION = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)-beta\.([1-9]\d*)$/;
const BASE64URL = /^(?:[A-Za-z0-9_-]{2,})$/;
const SHA512_BASE64 = /^(?:[A-Za-z0-9+/]{4}){21}[A-Za-z0-9+/]{2}==$/;
const MAX_MANIFEST_LIFETIME_MS = 7 * 24 * 60 * 60 * 1000;
const ED25519_SPKI_PREFIX = Buffer.from('302a300506032b6570032100', 'hex');

type BetaVersion = [bigint, bigint, bigint, bigint];
export interface VerifiedEnvelope { keyId: string; payloadBytes: Buffer; digest: string }
export interface BetaDefensiveState { schema: 1; channel: 'closed-beta'; version: string; sequence: number; manifestDigest: string; keyId: string }
export interface AcceptedBetaManifest {
  schema: 'vc-windows-update-v1'; channel: 'closed-beta'; keyId: string; version: string; platform: 'win32'; architecture: 'x64'; sequence: number;
  installerUrl: string; immutableUrl: string; size: number; sha256: string; sha512: string; publishedAt: string; notBefore: string; expiresAt: string; manifestDigest: string;
}
export interface BetaPolicyContext { currentVersion: string; platform: NodeJS.Platform; architecture: string; now: Date }

function exactKeys(value: Record<string, unknown>, expected: string[]): boolean {
  const keys = Object.keys(value).sort(); const wanted = [...expected].sort();
  return keys.length === wanted.length && keys.every((key, index) => key === wanted[index]);
}
function object(value: unknown): Record<string, unknown> {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error('value must be an object');
  return value as Record<string, unknown>;
}
class StrictJsonParser {
  private index = 0;
  constructor(private readonly text: string) {}
  parse(): unknown { const value = this.value(); this.space(); if (this.index !== this.text.length) throw new Error('invalid JSON'); return value; }
  private space(): void { while (this.index < this.text.length && /[\t\n\r ]/.test(this.text[this.index])) this.index++; }
  private value(): unknown {
    this.space(); const token = this.text[this.index];
    if (token === '{') return this.object(); if (token === '[') return this.array(); if (token === '"') return this.string();
    for (const [literal, value] of [['true', true], ['false', false], ['null', null]] as const) {
      if (this.text.startsWith(literal, this.index)) { this.index += literal.length; return value; }
    }
    const number = /-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?/.exec(this.text.slice(this.index));
    if (!number || !Number.isFinite(Number(number[0]))) throw new Error('invalid JSON'); this.index += number[0].length; return Number(number[0]);
  }
  private string(): string {
    const start = this.index++; let escaped = false;
    while (this.index < this.text.length) {
      const character = this.text[this.index++];
      if (!escaped && character === '"') { try { return JSON.parse(this.text.slice(start, this.index)) as string; } catch { throw new Error('invalid JSON'); } }
      if (!escaped && character.charCodeAt(0) < 0x20) throw new Error('invalid JSON');
      if (!escaped && character === '\\') escaped = true; else escaped = false;
    }
    throw new Error('invalid JSON');
  }
  private object(): Record<string, unknown> {
    this.index++; const result = Object.create(null) as Record<string, unknown>; const names = new Set<string>(); this.space();
    if (this.text[this.index] === '}') { this.index++; return result; }
    while (true) {
      this.space(); if (this.text[this.index] !== '"') throw new Error('invalid JSON'); const name = this.string();
      if (names.has(name)) throw new Error('duplicate JSON member'); names.add(name); this.space();
      if (this.text[this.index++] !== ':') throw new Error('invalid JSON'); result[name] = this.value(); this.space();
      const separator = this.text[this.index++]; if (separator === '}') return result; if (separator !== ',') throw new Error('invalid JSON');
    }
  }
  private array(): unknown[] {
    this.index++; const result: unknown[] = []; this.space(); if (this.text[this.index] === ']') { this.index++; return result; }
    while (true) { result.push(this.value()); this.space(); const separator = this.text[this.index++]; if (separator === ']') return result; if (separator !== ',') throw new Error('invalid JSON'); }
  }
}
function strictJson(bytes: Uint8Array): unknown {
  let text: string;
  try { text = new TextDecoder('utf-8', { fatal: true }).decode(bytes); } catch { throw new Error('invalid UTF-8'); }
  try { return new StrictJsonParser(text).parse(); } catch (error) { if ((error as Error).message === 'duplicate JSON member') throw error; throw new Error('invalid JSON'); }
}
function decodeBase64url(value: unknown, expectedLength?: number): Buffer {
  if (typeof value !== 'string' || !BASE64URL.test(value) || value.includes('=')) throw new Error('invalid base64url');
  const decoded = Buffer.from(value, 'base64url');
  if (decoded.toString('base64url') !== value || (expectedLength !== undefined && decoded.length !== expectedLength)) throw new Error('noncanonical base64url');
  return decoded;
}

/** Low-level deterministic-vector seam. Production callers must use verifyProductionBetaEnvelope. */
export function verifyEd25519Envelope(bytes: Uint8Array, publicKeyLookup: (keyId: string) => Buffer | undefined): VerifiedEnvelope {
  if (bytes.byteLength === 0 || bytes.byteLength > BETA_MANIFEST_MAX_BYTES) throw new Error('envelope size rejected');
  const envelope = object(strictJson(bytes));
  if (!exactKeys(envelope, ENVELOPE_KEYS) || envelope.schema !== 'vc-ed25519-envelope-v1' || typeof envelope.keyId !== 'string' || !/^[a-z0-9][a-z0-9-]{0,63}$/.test(envelope.keyId)) throw new Error('invalid envelope');
  const payloadBytes = decodeBase64url(envelope.payload); const signature = decodeBase64url(envelope.signature, 64);
  const rawKey = publicKeyLookup(envelope.keyId); if (!rawKey || rawKey.length !== 32) throw new Error('unknown key');
  const key = createPublicKey({ key: Buffer.concat([ED25519_SPKI_PREFIX, rawKey]), format: 'der', type: 'spki' });
  if (!verify(null, payloadBytes, key, signature)) throw new Error('invalid signature');
  return { keyId: envelope.keyId, payloadBytes, digest: createHash('sha256').update(payloadBytes).digest('hex') };
}

function betaVersion(value: unknown): BetaVersion | undefined {
  if (typeof value !== 'string') return;
  const match = BETA_VERSION.exec(value); return match ? [BigInt(match[1]), BigInt(match[2]), BigInt(match[3]), BigInt(match[4])] : undefined;
}
function compareVersion(left: BetaVersion, right: BetaVersion): number {
  for (let index = 0; index < left.length; index++) { if (left[index] > right[index]) return 1; if (left[index] < right[index]) return -1; }
  return 0;
}
function canonicalDate(value: unknown): Date | undefined {
  if (typeof value !== 'string') return;
  const date = new Date(value); return !Number.isNaN(date.valueOf()) && date.toISOString() === value ? date : undefined;
}
function exactBetaUrls(version: string): { installerUrl: string; immutableUrl: string } {
  const filename = `Void-Code-${version}-windows-x64.exe`;
  return {
    installerUrl: `https://vc.makscee.ru/download/windows/${filename}`,
    immutableUrl: `https://github.com/makscee/void-code/releases/download/desktop-v${version}/${filename}`,
  };
}

export function evaluateBetaPayload(verified: VerifiedEnvelope, context: BetaPolicyContext, state?: BetaDefensiveState): AcceptedBetaManifest {
  const raw = object(strictJson(verified.payloadBytes));
  if (!exactKeys(raw, PAYLOAD_KEYS) || raw.schema !== 'vc-windows-update-v1' || raw.channel !== 'closed-beta' || raw.keyId !== verified.keyId || raw.platform !== 'win32' || raw.architecture !== 'x64') throw new Error('payload schema rejected');
  if (context.platform !== 'win32' || context.architecture !== 'x64') throw new Error('runtime target rejected');
  const candidate = betaVersion(raw.version); const current = betaVersion(context.currentVersion);
  if (!candidate || !current) throw new Error('version rejected');
  const urls = exactBetaUrls(raw.version as string);
  if (raw.installerUrl !== urls.installerUrl || raw.immutableUrl !== urls.immutableUrl) throw new Error('URL rejected');
  if (!Number.isSafeInteger(raw.sequence) || Number(raw.sequence) < 1 || !Number.isSafeInteger(raw.size) || Number(raw.size) < 1 || Number(raw.size) > BETA_INSTALLER_MAX_BYTES) throw new Error('numeric bound rejected');
  if (typeof raw.sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(raw.sha256) || typeof raw.sha512 !== 'string' || !SHA512_BASE64.test(raw.sha512)) throw new Error('hash rejected');
  const published = canonicalDate(raw.publishedAt); const notBefore = canonicalDate(raw.notBefore); const expires = canonicalDate(raw.expiresAt);
  if (!published || !notBefore || !expires || published > notBefore || notBefore > expires || expires.valueOf() - published.valueOf() > MAX_MANIFEST_LIFETIME_MS || context.now < notBefore || context.now > expires) throw new Error('time policy rejected');
  const versionOrder = compareVersion(candidate, current);
  if (versionOrder < 0) throw new Error('downgrade rejected');
  if (versionOrder === 0 && (!state || state.version !== raw.version || state.manifestDigest !== verified.digest || state.keyId !== verified.keyId || state.sequence !== raw.sequence)) throw new Error('same-version replay rejected');
  if (state) {
    const stateVersion = betaVersion(state.version); if (!stateVersion) throw new Error('invalid defensive state');
    const stateOrder = compareVersion(candidate, stateVersion);
    if (stateOrder < 0 || Number(raw.sequence) < state.sequence) throw new Error('rollback rejected');
    if (stateOrder === 0 && (verified.digest !== state.manifestDigest || Number(raw.sequence) !== state.sequence || verified.keyId !== state.keyId)) throw new Error('replay rejected');
    if (stateOrder > 0 && Number(raw.sequence) <= state.sequence) throw new Error('sequence rejected');
  }
  return { ...(raw as Omit<AcceptedBetaManifest, 'manifestDigest'>), manifestDigest: verified.digest } as AcceptedBetaManifest;
}

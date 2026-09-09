import { createHash, createPublicKey, verify } from 'node:crypto';

const MAX_ENVELOPE_BYTES = 64 * 1024;
const MAX_PAYLOAD_BYTES = 32 * 1024;
const MAX_ARTIFACTS = 32;
const MAX_ARTIFACT_SIZE = 1024 * 1024 * 1024;
const PRODUCT = 'works.voidcode.desktop';
const CHANNEL = 'stable';

export type DesktopUpdateArtifact = {
  platform: string;
  arch: string;
  file: string;
  size: number;
  sha256: string;
};

export type DesktopInstalledBuild = {
  product: string;
  channel: string;
  version: string;
  platform: string;
  arch: string;
  packaged: boolean;
};

export type DesktopUpdatePlan = {
  version: string;
  payloadDigest: string;
  artifact: DesktopUpdateArtifact;
  payloadBytes: Uint8Array;
};

export type VerifiedDesktopUpdatePayload = {
  payloadBytes: Uint8Array;
  payloadDigest: string;
  keyId: string;
};

export type DesktopUpdateVerificationResult =
  | { ok: true; verified: VerifiedDesktopUpdatePayload }
  | { ok: false; code: string };

export type DesktopUpdateSelectionResult =
  | { ok: true; plan: DesktopUpdatePlan }
  | { ok: false; code: string };

type JsonObject = Record<string, unknown>;
type StableVersion = readonly [number, number, number];

class StrictJsonParser {
  private position = 0;

  public constructor(private readonly source: string) {}

  public parse(): unknown {
    this.skipWhitespace();
    const value = this.parseValue();
    this.skipWhitespace();
    if (this.position !== this.source.length) {
      throw new Error('trailing JSON input');
    }
    return value;
  }

  private parseValue(): unknown {
    const current = this.source[this.position];
    if (current === '{') return this.parseObject();
    if (current === '[') return this.parseArray();
    if (current === '"') return this.parseString();
    if (current === 't') return this.parseKeyword('true', true);
    if (current === 'f') return this.parseKeyword('false', false);
    if (current === 'n') return this.parseKeyword('null', null);
    return this.parseNumber();
  }

  private parseObject(): JsonObject {
    this.position += 1;
    this.skipWhitespace();
    const result = Object.create(null) as JsonObject;
    const keys = new Set<string>();
    if (this.consume('}')) return result;

    while (true) {
      if (this.source[this.position] !== '"') throw new Error('invalid object key');
      const key = this.parseString();
      if (keys.has(key)) throw new Error('duplicate object key');
      keys.add(key);
      this.skipWhitespace();
      if (!this.consume(':')) throw new Error('missing object colon');
      this.skipWhitespace();
      result[key] = this.parseValue();
      this.skipWhitespace();
      if (this.consume('}')) return result;
      if (!this.consume(',')) throw new Error('missing object comma');
      this.skipWhitespace();
    }
  }

  private parseArray(): unknown[] {
    this.position += 1;
    this.skipWhitespace();
    const result: unknown[] = [];
    if (this.consume(']')) return result;

    while (true) {
      result.push(this.parseValue());
      this.skipWhitespace();
      if (this.consume(']')) return result;
      if (!this.consume(',')) throw new Error('missing array comma');
      this.skipWhitespace();
    }
  }

  private parseString(): string {
    const start = this.position;
    this.position += 1;
    let escaped = false;
    while (this.position < this.source.length) {
      const character = this.source.charCodeAt(this.position);
      if (!escaped && character === 0x22) {
        this.position += 1;
        const value: unknown = JSON.parse(this.source.slice(start, this.position));
        if (typeof value !== 'string') throw new Error('invalid JSON string');
        return value;
      }
      if (!escaped && character < 0x20) throw new Error('control character in string');
      if (!escaped && character === 0x5c) {
        escaped = true;
      } else {
        escaped = false;
      }
      this.position += 1;
    }
    throw new Error('unterminated JSON string');
  }

  private parseKeyword<T>(keyword: string, value: T): T {
    if (this.source.slice(this.position, this.position + keyword.length) !== keyword) {
      throw new Error('invalid JSON keyword');
    }
    this.position += keyword.length;
    return value;
  }

  private parseNumber(): number {
    const remainder = this.source.slice(this.position);
    const match = /^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?/.exec(remainder);
    if (match === null) throw new Error('invalid JSON value');
    this.position += match[0].length;
    const value: unknown = JSON.parse(match[0]);
    if (typeof value !== 'number') throw new Error('invalid JSON number');
    return value;
  }

  private skipWhitespace(): void {
    while (/\s/.test(this.source[this.position] ?? '') && this.position < this.source.length) {
      const character = this.source[this.position];
      if (character !== ' ' && character !== '\n' && character !== '\r' && character !== '\t') {
        throw new Error('invalid JSON whitespace');
      }
      this.position += 1;
    }
  }

  private consume(character: string): boolean {
    if (this.source[this.position] !== character) return false;
    this.position += 1;
    return true;
  }
}

function decodeStrictJson(bytes: Uint8Array): unknown {
  const text = new TextDecoder('utf-8', { fatal: true }).decode(bytes);
  return new StrictJsonParser(text).parse();
}

function isObject(value: unknown): value is JsonObject {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function hasExactKeys(value: JsonObject, expected: readonly string[]): boolean {
  const keys = Object.keys(value);
  return keys.length === expected.length && expected.every((key) => Object.hasOwn(value, key));
}

function decodeCanonicalBase64(value: string): Uint8Array | undefined {
  if (value.length === 0 || value.length % 4 !== 0) return undefined;
  if (!/^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/.test(value)) {
    return undefined;
  }
  const decoded = Buffer.from(value, 'base64');
  if (decoded.toString('base64') !== value) return undefined;
  return new Uint8Array(decoded);
}

export function parseStableDesktopVersion(value: unknown): StableVersion | undefined {
  if (typeof value !== 'string') return undefined;
  const match = /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.exec(value);
  if (match === null) return undefined;
  const parts = match.slice(1).map(Number);
  if (!parts.every(Number.isSafeInteger)) return undefined;
  return [parts[0]!, parts[1]!, parts[2]!];
}

export function compareStableDesktopVersions(left: string, right: string): number | undefined {
  const leftParts = parseStableDesktopVersion(left);
  const rightParts = parseStableDesktopVersion(right);
  if (leftParts === undefined || rightParts === undefined) return undefined;
  for (let index = 0; index < leftParts.length; index += 1) {
    if (leftParts[index]! < rightParts[index]!) return -1;
    if (leftParts[index]! > rightParts[index]!) return 1;
  }
  return 0;
}

export function isSupportedDesktopInstalledBuild(installed: DesktopInstalledBuild): boolean {
  if (installed.product !== PRODUCT || installed.channel !== CHANNEL || installed.packaged !== true) return false;
  if (parseStableDesktopVersion(installed.version) === undefined) return false;
  const target = `${installed.platform}/${installed.arch}`;
  return target === 'win32/x64' || target === 'darwin/x64' || target === 'darwin/arm64';
}

function knownArtifactFilename(platform: string, arch: string): string | undefined {
  const target = `${platform}/${arch}`;
  if (target === 'win32/x64') return 'Void-Code-windows-x64.exe';
  if (target === 'darwin/x64') return 'void-code-mac-x64.zip';
  if (target === 'darwin/arm64') return 'void-code-mac-arm64.zip';
  return undefined;
}

function parseArtifact(value: unknown): DesktopUpdateArtifact | undefined {
  if (!isObject(value) || !hasExactKeys(value, ['platform', 'arch', 'file', 'size', 'sha256'])) return undefined;
  const { platform, arch, file, size, sha256 } = value;
  if (typeof platform !== 'string' || !/^[a-z0-9][a-z0-9-]{0,31}$/.test(platform)) return undefined;
  if (typeof arch !== 'string' || !/^[a-z0-9][a-z0-9-]{0,31}$/.test(arch)) return undefined;
  if (typeof file !== 'string' || file.length === 0 || file.length > 128) return undefined;
  if (!/^[A-Za-z0-9][A-Za-z0-9._-]*$/.test(file) || file === '.' || file === '..') return undefined;
  if (typeof size !== 'number' || !Number.isSafeInteger(size) || size < 1 || size > MAX_ARTIFACT_SIZE) return undefined;
  if (typeof sha256 !== 'string' || !/^[a-f0-9]{64}$/.test(sha256)) return undefined;
  const expectedFilename = knownArtifactFilename(platform, arch);
  if (expectedFilename !== undefined && file !== expectedFilename) return undefined;
  return { platform, arch, file, size, sha256 };
}

export function verifyDesktopUpdateEnvelope(
  raw: Uint8Array,
  keyring: Readonly<Record<string, string | Uint8Array>>,
): DesktopUpdateVerificationResult {
  if (!(raw instanceof Uint8Array) || raw.byteLength === 0 || raw.byteLength > MAX_ENVELOPE_BYTES) {
    return { ok: false, code: 'invalid-envelope-size' };
  }

  try {
    const envelope = decodeStrictJson(raw);
    if (!isObject(envelope) || !hasExactKeys(envelope, ['schema', 'keyId', 'payload', 'signature'])) {
      return { ok: false, code: 'invalid-envelope-shape' };
    }
    if (envelope.schema !== 1 || typeof envelope.keyId !== 'string' ||
        !/^[A-Za-z0-9._-]{1,64}$/.test(envelope.keyId) ||
        typeof envelope.payload !== 'string' || typeof envelope.signature !== 'string') {
      return { ok: false, code: 'invalid-envelope-fields' };
    }
    if (!Object.hasOwn(keyring, envelope.keyId)) return { ok: false, code: 'unknown-key' };

    const payloadBytes = decodeCanonicalBase64(envelope.payload);
    const signatureBytes = decodeCanonicalBase64(envelope.signature);
    if (payloadBytes === undefined || payloadBytes.byteLength > MAX_PAYLOAD_BYTES) {
      return { ok: false, code: 'invalid-payload-encoding' };
    }
    if (signatureBytes === undefined || signatureBytes.byteLength !== 64) {
      return { ok: false, code: 'invalid-signature-encoding' };
    }

    const encodedKey = keyring[envelope.keyId];
    if (encodedKey === undefined) return { ok: false, code: 'unknown-key' };
    const publicKey = createPublicKey(typeof encodedKey === 'string' ? encodedKey : Buffer.from(encodedKey));
    if (!verify(null, Buffer.from(payloadBytes), publicKey, Buffer.from(signatureBytes))) {
      return { ok: false, code: 'invalid-signature' };
    }
    // Parse only after authenticating, and never reserialize the authenticated octets.
    decodeStrictJson(payloadBytes);

    const payloadCopy = new Uint8Array(payloadBytes);
    return {
      ok: true,
      verified: {
        payloadBytes: payloadCopy,
        payloadDigest: createHash('sha256').update(payloadCopy).digest('hex'),
        keyId: envelope.keyId,
      },
    };
  } catch {
    return { ok: false, code: 'invalid-envelope' };
  }
}

export function parseAndSelectDesktopRelease(
  payloadBytes: Uint8Array,
  installed: DesktopInstalledBuild,
): DesktopUpdateSelectionResult {
  if (!(payloadBytes instanceof Uint8Array) || payloadBytes.byteLength === 0 || payloadBytes.byteLength > MAX_PAYLOAD_BYTES) {
    return { ok: false, code: 'invalid-payload-size' };
  }
  if (!isSupportedDesktopInstalledBuild(installed)) return { ok: false, code: 'unsupported-installed-build' };

  try {
    const manifest = decodeStrictJson(payloadBytes);
    if (!isObject(manifest) ||
        !hasExactKeys(manifest, ['schema', 'product', 'channel', 'version', 'tag', 'artifacts'])) {
      return { ok: false, code: 'invalid-manifest-shape' };
    }
    if (manifest.schema !== 1 || manifest.product !== PRODUCT || manifest.channel !== CHANNEL ||
        typeof manifest.version !== 'string' || typeof manifest.tag !== 'string' ||
        manifest.tag !== `v${manifest.version}` || parseStableDesktopVersion(manifest.version) === undefined ||
        !Array.isArray(manifest.artifacts) || manifest.artifacts.length < 1 || manifest.artifacts.length > MAX_ARTIFACTS) {
      return { ok: false, code: 'invalid-manifest-fields' };
    }

    const artifacts: DesktopUpdateArtifact[] = [];
    const bindings = new Set<string>();
    const filenames = new Set<string>();
    for (const value of manifest.artifacts) {
      const artifact = parseArtifact(value);
      if (artifact === undefined) return { ok: false, code: 'invalid-artifact' };
      const binding = `${artifact.platform}\u0000${artifact.arch}`;
      const filename = artifact.file.toLowerCase();
      if (bindings.has(binding) || filenames.has(filename)) {
        return { ok: false, code: 'ambiguous-artifact' };
      }
      bindings.add(binding);
      filenames.add(filename);
      artifacts.push(artifact);
    }

    const comparison = compareStableDesktopVersions(manifest.version, installed.version);
    if (comparison === undefined) return { ok: false, code: 'invalid-version' };
    if (comparison <= 0) return { ok: false, code: 'not-newer' };
    const selected = artifacts.find((artifact) =>
      artifact.platform === installed.platform && artifact.arch === installed.arch,
    );
    if (selected === undefined) return { ok: false, code: 'unsupported-target' };

    const payloadCopy = new Uint8Array(payloadBytes);
    return {
      ok: true,
      plan: {
        version: manifest.version,
        payloadDigest: createHash('sha256').update(payloadCopy).digest('hex'),
        artifact: { ...selected },
        payloadBytes: payloadCopy,
      },
    };
  } catch {
    return { ok: false, code: 'invalid-manifest' };
  }
}

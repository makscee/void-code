import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import {
  BETA_MANIFEST_MAX_BYTES,
  type BetaDefensiveState,
  type VerifiedEnvelope,
  evaluateBetaPayload,
  verifyEd25519Envelope,
} from '../src/main/ed25519-beta';

const vector = JSON.parse(readFileSync(new URL('./fixtures/ed25519-rfc8032-test-2.json', import.meta.url), 'utf8')) as { publicKeyHex: string; payloadBase64url: string; signatureBase64url: string };
const rfc8032 = { keyId: 'rfc8032-test-2', publicKey: vector.publicKeyHex, payload: vector.payloadBase64url, signature: vector.signatureBase64url };
const envelope = (overrides: Record<string, unknown> = {}) => Buffer.from(JSON.stringify({
  schema: 'vc-ed25519-envelope-v1', keyId: rfc8032.keyId, payload: rfc8032.payload, signature: rfc8032.signature, ...overrides,
}));
const lookup = (keyId: string) => keyId === rfc8032.keyId ? Buffer.from(rfc8032.publicKey, 'hex') : undefined;

function payload(overrides: Record<string, unknown> = {}) {
  return {
    schema: 'vc-windows-update-v1', channel: 'closed-beta', keyId: 'test-beta-2026-01', version: '0.1.3-beta.2',
    platform: 'win32', architecture: 'x64', sequence: 2,
    installerUrl: 'https://vc.makscee.ru/download/windows/Void-Code-0.1.3-beta.2-windows-x64.exe',
    immutableUrl: 'https://github.com/makscee/void-code/releases/download/desktop-v0.1.3-beta.2/Void-Code-0.1.3-beta.2-windows-x64.exe',
    size: 42, sha256: 'a'.repeat(64), sha512: Buffer.alloc(64, 1).toString('base64'),
    publishedAt: '2026-08-17T00:00:00.000Z', notBefore: '2026-08-17T00:00:00.000Z', expiresAt: '2026-08-20T00:00:00.000Z',
    ...overrides,
  };
}
function verified(value: unknown): VerifiedEnvelope {
  const bytes = Buffer.from(JSON.stringify(value));
  return { payloadBytes: bytes, keyId: 'test-beta-2026-01', digest: createHash('sha256').update(bytes).digest('hex') };
}
const context = { currentVersion: '0.1.3-beta.1', platform: 'win32' as const, architecture: 'x64', now: new Date('2026-08-18T00:00:00.000Z') };

 describe('Ed25519 exact-byte envelope', () => {
  it('matches the published RFC 8032 test vector without reserializing payload bytes', () => {
    expect(verifyEd25519Envelope(envelope(), lookup)).toEqual({
      keyId: rfc8032.keyId, payloadBytes: Buffer.from([0x72]), digest: createHash('sha256').update(Buffer.from([0x72])).digest('hex'),
    });
  });

  it.each([
    ['unknown key', { keyId: 'unknown' }],
    ['wrong signature', { signature: `A${rfc8032.signature.slice(1)}` }],
    ['tampered payload', { payload: 'cw' }],
    ['padding', { payload: 'cg==' }],
    ['alphabet', { payload: 'c+' }],
    ['short signature', { signature: 'AA' }],
    ['unknown envelope member', { extra: true }],
  ])('rejects %s', (_name, change) => expect(() => verifyEd25519Envelope(envelope(change), lookup)).toThrow());

  it('rejects duplicate member names at every envelope depth, including escaped-equivalent names', () => {
    for (const json of [
      `{"schema":"vc-ed25519-envelope-v1","keyId":"${rfc8032.keyId}","payload":"${rfc8032.payload}","signature":"${rfc8032.signature}","keyId":"${rfc8032.keyId}"}`,
      `{"schema":"vc-ed25519-envelope-v1","keyId":"${rfc8032.keyId}","payload":"${rfc8032.payload}","signature":"${rfc8032.signature}","nested":{"name":1,"name":2}}`,
      `{"schema":"vc-ed25519-envelope-v1","keyId":"${rfc8032.keyId}","payload":"${rfc8032.payload}","signature":"${rfc8032.signature}","nested":{"keyId":1,"key\\u0049d":2}}`,
    ]) expect(() => verifyEd25519Envelope(Buffer.from(json), lookup)).toThrow(/duplicate JSON member/);
  });

  it('rejects oversized and non-UTF8/non-object envelopes', () => {
    expect(() => verifyEd25519Envelope(Buffer.alloc(BETA_MANIFEST_MAX_BYTES + 1), lookup)).toThrow();
    for (const bytes of [Buffer.from([0xff]), Buffer.from('[]'), Buffer.from('{}'), Buffer.from('{')]) {
      expect(() => verifyEd25519Envelope(bytes, lookup)).toThrow();
    }
  });
});

describe('closed-beta payload policy', () => {
  it('accepts the exact schema and permits same-version idempotency only by exact digest', () => {
    const result = evaluateBetaPayload(verified(payload()), context);
    expect(result.version).toBe('0.1.3-beta.2');
    const state: BetaDefensiveState = { schema: 1, channel: 'closed-beta', version: result.version, sequence: result.sequence, manifestDigest: result.manifestDigest, keyId: result.keyId };
    expect(evaluateBetaPayload(verified(payload()), { ...context, currentVersion: result.version }, state)).toEqual(result);
    expect(() => evaluateBetaPayload(verified(payload({ sha256: 'b'.repeat(64) })), { ...context, currentVersion: result.version }, state)).toThrow();
  });

  it.each([
    ['unknown member', { extra: true }], ['schema', { schema: 'other' }], ['channel', { channel: 'stable' }],
    ['key mismatch', { keyId: 'other' }], ['version stable', { version: '0.1.4' }], ['version malformed', { version: '01.1.3-beta.2' }],
    ['platform', { platform: 'darwin' }], ['architecture', { architecture: 'arm64' }], ['sequence', { sequence: 0 }],
    ['foreign URL', { installerUrl: 'https://evil.example/update.exe' }], ['URL query', { installerUrl: 'https://vc.makscee.ru/download/windows/Void-Code-0.1.3-beta.2-windows-x64.exe?x=1' }],
    ['immutable URL', { immutableUrl: 'https://github.com/other/update.exe' }], ['size zero', { size: 0 }], ['size unsafe', { size: Number.MAX_SAFE_INTEGER + 1 }],
    ['hash', { sha256: 'A'.repeat(64) }], ['sha512', { sha512: 'x' }],
    ['not yet valid', { notBefore: '2026-08-19T00:00:00.000Z' }], ['expired', { expiresAt: '2026-08-17T12:00:00.000Z' }],
    ['noncanonical date', { publishedAt: '2026-08-17T00:00:00Z' }], ['reversed dates', { publishedAt: '2026-08-18T00:00:00.000Z', notBefore: '2026-08-17T00:00:00.000Z' }],
    ['overlong lifetime', { expiresAt: '2026-08-25T00:00:00.000Z' }],
  ])('rejects %s', (_name, change) => expect(() => evaluateBetaPayload(verified(payload(change)), context)).toThrow());

  it('rejects downgrade, replay, wrong host runtime, and non-monotonic sequence', () => {
    expect(() => evaluateBetaPayload(verified(payload({ version: '0.1.3-beta.0' })), context)).toThrow();
    expect(() => evaluateBetaPayload(verified(payload()), { ...context, platform: 'darwin' })).toThrow();
    const state: BetaDefensiveState = { schema: 1, channel: 'closed-beta', version: '0.1.3-beta.3', sequence: 3, manifestDigest: 'b'.repeat(64), keyId: 'test-beta-2026-01' };
    expect(() => evaluateBetaPayload(verified(payload()), context, state)).toThrow();
    expect(() => evaluateBetaPayload(verified(payload({ version: '0.1.3-beta.4', sequence: 3 })), context, state)).toThrow();
  });

  it('rejects duplicate payload member names at the root and nested depths, including escaped-equivalent names', () => {
    const base = JSON.stringify(payload());
    const rootDuplicate = base.replace('"schema":"vc-windows-update-v1"', '"schema":"vc-windows-update-v1","schema":"vc-windows-update-v1"');
    const nestedDuplicate = base.replace('"architecture":"x64"', '"architecture":"x64","probe":{"name":1,"name":2}');
    const escapedDuplicate = base.replace('"architecture":"x64"', '"architecture":"x64","probe":{"keyId":1,"key\\u0049d":2}');
    for (const text of [rootDuplicate, nestedDuplicate, escapedDuplicate]) {
      const bytes = Buffer.from(text); const candidate = { payloadBytes: bytes, keyId: payload().keyId, digest: createHash('sha256').update(bytes).digest('hex') };
      expect(() => evaluateBetaPayload(candidate, context)).toThrow(/duplicate JSON member/);
    }
  });

  it('fails closed across deterministic malformed payload samples', () => {
    const samples: unknown[] = [null, [], '', 1, {}, ...Array.from({ length: 32 }, (_, index) => ({ ...payload(), [`x${index}`]: index }))];
    for (const sample of samples) expect(() => evaluateBetaPayload(verified(sample), context)).toThrow();
  });
});

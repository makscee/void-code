import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { assembleClosedBetaEnvelope, buildClosedBetaPayload, payloadSha256 } from '../scripts/closed-beta-manifest-lib.mjs';

const input = (version: string, sequence: number) => ({
  version, keyId: 'beta-2026-08', sequence, size: 170186363,
  sha256: 'a'.repeat(64), sha512: 'A'.repeat(86) + '==',
  publishedAt: '2026-08-17T12:00:00.000Z', notBefore: '2026-08-17T12:00:00.000Z', expiresAt: '2026-08-24T12:00:00.000Z',
});

describe('closed-beta ceremony artifact tooling', () => {
  it.each([['0.1.3-beta.3', 3], ['0.1.3-beta.4', 4], ['0.1.3-beta.5', 5]])('builds immutable %s payload bytes', (version, sequence) => {
    const payload = buildClosedBetaPayload(input(version, sequence));
    const value = JSON.parse(payload.toString('utf8'));
    expect(value).toMatchObject({ channel: 'closed-beta', version, sequence, keyId: 'beta-2026-08', platform: 'win32', architecture: 'x64' });
    expect(value.installerUrl).toBe(`https://vc.makscee.ru/download/windows/Void-Code-${version}-windows-x64.exe`);
    expect(value.immutableUrl).toBe(`https://github.com/makscee/void-code/releases/download/desktop-v${version}/Void-Code-${version}-windows-x64.exe`);
    expect(payloadSha256(payload)).toMatch(/^[0-9a-f]{64}$/);
  });

  it('rejects stable identities, altered lifetimes, and non-exact artifact fields', () => {
    expect(() => buildClosedBetaPayload(input('0.1.3', 3))).toThrow();
    expect(() => buildClosedBetaPayload({ ...input('0.1.3-beta.3', 3), expiresAt: '2026-08-25T12:00:00.000Z' })).toThrow();
    expect(() => buildClosedBetaPayload({ ...input('0.1.3-beta.3', 3), sha256: 'A'.repeat(64) })).toThrow();
  });

  it('assembles only a signer-produced 64-byte signature without altering exact payload bytes', () => {
    const payload = buildClosedBetaPayload(input('0.1.3-beta.3', 3));
    const vector = JSON.parse(readFileSync(new URL('./fixtures/ed25519-rfc8032-test-2.json', import.meta.url), 'utf8')) as { signatureBase64url: string };
    const envelope = JSON.parse(assembleClosedBetaEnvelope(payload, Buffer.from(vector.signatureBase64url, 'base64url'), 'beta-2026-08').toString('utf8'));
    expect(Buffer.from(envelope.payload, 'base64url')).toEqual(payload);
    expect(envelope.signature).toBe(vector.signatureBase64url);
    expect(() => assembleClosedBetaEnvelope(payload, Buffer.alloc(63), 'beta-2026-08')).toThrow();
  });
});

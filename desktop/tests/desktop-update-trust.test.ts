import { existsSync } from 'node:fs';
import fc from 'fast-check';
import { describe, expect, it } from 'vitest';
import {
  artifactFor,
  decoder,
  defaultArtifact,
  encoder,
  envelope,
  installed,
  keyring,
  manifest,
  payloadBytes,
  replaceEnvelopeField,
  sha256,
  signedRawPayload,
  testKeyId,
  type Artifact,
  type TrustModule,
} from './desktop-update-fixtures.ts';

async function trust(): Promise<TrustModule> {
  const file = new URL('../src/main/desktop-update-trust.ts', import.meta.url);
  expect(existsSync(file), 'desktop update trust is not implemented').toBe(true);
  return import(file.href) as Promise<TrustModule>;
}
async function verified(raw: Uint8Array): Promise<ReturnType<TrustModule['verifyDesktopUpdateEnvelope']>> {
  return (await trust()).verifyDesktopUpdateEnvelope(raw, keyring);
}
async function selected(value = manifest(), build = installed()): Promise<ReturnType<TrustModule['parseAndSelectDesktopRelease']>> {
  return (await trust()).parseAndSelectDesktopRelease(payloadBytes(value), build);
}
async function selectedRaw(value: unknown, build = installed()): Promise<ReturnType<TrustModule['parseAndSelectDesktopRelease']>> {
  return (await trust()).parseAndSelectDesktopRelease(encoder.encode(JSON.stringify(value)), build);
}
function envelopeStrings(raw: Uint8Array): { payload: string; signature: string } {
  const parsed = JSON.parse(decoder.decode(raw)) as { payload: string; signature: string };
  return parsed;
}
function nonCanonicalPadBits(base64: string): string {
  const alphabet = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/';
  const padding = base64.match(/=+$/)?.[0].length ?? 0;
  const last = base64.length - padding - 1;
  const index = alphabet.indexOf(base64[last]!);
  const keepMask = padding === 2 ? 0b110000 : 0b111100;
  return `${base64.slice(0, last)}${alphabet[(index & keepMask) | 1]}${base64.slice(last + 1)}`;
}

// R1: signatures authenticate exactly the decoded payload octets, never a reserialized JSON value.
describe('desktop update signed envelope trust boundary', () => {
  it('accepts fixture Ed25519 signatures over original decoded payload bytes', async () => {
    const result = await verified(envelope());
    expect(result).toMatchObject({ ok: true, verified: { keyId: testKeyId, payloadDigest: sha256(payloadBytes()) } });
    if (result.ok) expect([...result.verified.payloadBytes]).toEqual([...payloadBytes()]);
  });

  it('accepts a non-canonical JSON payload when its exact original bytes were signed', async () => {
    const rawPayload = '{\n  "artifacts" : [ { "sha256" : "' + sha256(new Uint8Array([1, 2, 3, 4])) + '", "size" : 4, "file" : "Void-Code-windows-x64.exe", "arch" : "x64", "platform" : "win32" } ],\n  "tag":"v1.2.3", "version":"1.2.3", "channel":"stable", "product":"works.voidcode.desktop", "schema":1\n}';
    const result = await verified(signedRawPayload(rawPayload));
    expect(result.ok).toBe(true);
    if (result.ok) expect(decoder.decode(result.verified.payloadBytes)).toBe(rawPayload);
  });

  it('rejects every generated single-bit payload mutation without re-signing', async () => {
    const original = envelope();
    const payload = Buffer.from(envelopeStrings(original).payload, 'base64');
    await fc.assert(fc.asyncProperty(
      fc.integer({ min: 0, max: payload.length - 1 }),
      fc.integer({ min: 0, max: 7 }),
      async (index, bit) => {
        const mutated = Buffer.from(payload);
        mutated[index] = mutated[index]! ^ (1 << bit);
        const raw = replaceEnvelopeField(original, 'payload', mutated.toString('base64'));
        expect((await verified(raw)).ok, `mutated payload byte ${index}`).toBe(false);
      },
    ), { numRuns: 40 });
  });

  it('rejects every generated single-bit signature mutation without re-signing', async () => {
    const original = envelope();
    const signature = Buffer.from(envelopeStrings(original).signature, 'base64');
    await fc.assert(fc.asyncProperty(
      fc.integer({ min: 0, max: signature.length - 1 }),
      fc.integer({ min: 0, max: 7 }),
      async (index, bit) => {
        const mutated = Buffer.from(signature);
        mutated[index] = mutated[index]! ^ (1 << bit);
        const raw = replaceEnvelopeField(original, 'signature', mutated.toString('base64'));
        expect((await verified(raw)).ok, `mutated signature byte ${index}`).toBe(false);
      },
    ), { numRuns: 40 });
  });

  it.each([
    ['unknown key', envelope(payloadBytes(), { keyId: 'unknown-key' })],
    ['empty key', envelope(payloadBytes(), { keyId: '' })],
    ['short signature', envelope(payloadBytes(), { signature: new Uint8Array(63) })],
    ['wrong schema', envelope(payloadBytes(), { schema: 2 })],
    ['extra envelope member', envelope(payloadBytes(), { extra: { surprise: true } })],
  ])('rejects %s before it can yield verified payload', async (_label, raw) => {
    const result = await verified(raw);
    expect(result.ok).toBe(false);
    expect(result).not.toHaveProperty('verified');
  });

  it('rejects non-canonical base64 spellings while an otherwise identical valid envelope verifies', async () => {
    const shortPayload = encoder.encode('{}');
    const valid = envelope(shortPayload);
    const { payload, signature } = envelopeStrings(valid);
    expect((await verified(valid)).ok).toBe(true);

    // This valid JSON spelling contains `/` in canonical base64, so only its alphabet differs.
    const urlPayload = envelope(encoder.encode('{"x":"???"}'));
    const canonicalUrlPayload = envelopeStrings(urlPayload).payload;
    const urlSpelling = canonicalUrlPayload.replace('/', '_');
    expect((await verified(urlPayload)).ok).toBe(true);
    expect(urlSpelling).not.toBe(canonicalUrlPayload);
    expect([...Buffer.from(urlSpelling.replace('_', '/'), 'base64')]).toEqual([...Buffer.from(canonicalUrlPayload, 'base64')]);
    const variants: Array<[string, Uint8Array]> = [
      ['payload whitespace', replaceEnvelopeField(valid, 'payload', `${payload} `)],
      ['payload unpadded', replaceEnvelopeField(valid, 'payload', payload.replace(/=+$/, ''))],
      ['payload pad bits', replaceEnvelopeField(valid, 'payload', nonCanonicalPadBits(payload))],
      ['signature whitespace', replaceEnvelopeField(valid, 'signature', `${signature}\n`)],
      ['signature unpadded', replaceEnvelopeField(valid, 'signature', signature.replace(/=+$/, ''))],
      ['signature pad bits', replaceEnvelopeField(valid, 'signature', nonCanonicalPadBits(signature))],
      ['base64url payload', replaceEnvelopeField(urlPayload, 'payload', urlSpelling)],
    ];
    for (const [label, raw] of variants) {
      expect((await verified(raw)).ok, label).toBe(false);
    }
  });

  it.each([
    ['duplicate envelope member', () => {
      const valid = envelope();
      const { payload, signature } = envelopeStrings(valid);
      return encoder.encode(`{"schema":1,"schema":1,"keyId":"${testKeyId}","payload":"${payload}","signature":"${signature}"}`);
    }],
    ['missing envelope member', () => {
      const { payload } = envelopeStrings(envelope());
      return encoder.encode(`{"schema":1,"keyId":"${testKeyId}","payload":"${payload}"}`);
    }],
    ['malformed envelope UTF-8', () => new Uint8Array([0xff])],
    ['malformed payload UTF-8 signed correctly', () => envelope(new Uint8Array([0xff]))],
    ['duplicate payload member signed correctly', () => signedRawPayload('{"schema":1,"schema":1}')],
    ['envelope over 64 KiB', () => new Uint8Array(65 * 1024)],
    ['decoded payload over 32 KiB signed correctly', () => envelope(new Uint8Array(33 * 1024))],
  ])('fails closed on %s', async (_label, build) => {
    expect((await verified(build())).ok).toBe(false);
  });
});

describe('desktop update manifest selection', () => {
  it.each([
    ['wrong product', { product: 'works.voidcode.cli' }],
    ['wrong channel', { channel: 'beta' }],
    ['leading zero', { version: '01.2.3', tag: 'v01.2.3' }],
    ['v prefix', { version: 'v1.2.3', tag: 'vv1.2.3' }],
    ['prerelease', { version: '1.2.3-rc.1', tag: 'v1.2.3-rc.1' }],
    ['build metadata', { version: '1.2.3+build', tag: 'v1.2.3+build' }],
    ['tag mismatch', { tag: 'v9.9.9' }],
  ])('rejects manifest %s', async (_label, patch) => {
    expect((await selected(manifest(patch))).ok).toBe(false);
  });

  it.each([
    ['win32 x64', installed(), defaultArtifact()],
    ['darwin arm64', installed({ platform: 'darwin', arch: 'arm64' }), artifactFor(undefined, { platform: 'darwin', arch: 'arm64', file: 'void-code-mac-arm64.zip' })],
    ['darwin x64', installed({ platform: 'darwin', arch: 'x64' }), artifactFor(undefined, { platform: 'darwin', arch: 'x64', file: 'void-code-mac-x64.zip' })],
  ])('selects exact installed %s artifact', async (_label, build, artifact) => {
    const result = await selected(manifest({ artifacts: [artifact] }), build);
    expect(result).toMatchObject({ ok: true, plan: { artifact } });
  });

  it.each([
    ['missing manifest key', () => { const value = { ...manifest() } as Record<string, unknown>; delete value.tag; return value; }],
    ['extra manifest key', () => ({ ...manifest(), surprise: true })],
    ['wrong manifest primitive', () => ({ ...manifest(), schema: '1' })],
    ['missing artifact key', () => ({ ...manifest(), artifacts: [{ platform: 'win32', arch: 'x64', file: 'Void-Code-windows-x64.exe', size: 4 }] })],
    ['extra artifact key', () => ({ ...manifest(), artifacts: [{ ...defaultArtifact(), extra: 1 }] })],
    ['wrong artifact primitive', () => ({ ...manifest(), artifacts: [{ ...defaultArtifact(), size: '4' }] })],
    ['zero artifacts', () => ({ ...manifest(), artifacts: [] })],
  ])('rejects strict shape: %s', async (_label, build) => {
    expect((await selectedRaw(build())).ok).toBe(false);
  });

  const unknownArtifact = (platform = 'future', arch = 'x64', file = 'Void-Code-future-x64.exe'): Artifact =>
    artifactFor(undefined, { platform, arch, file });

  it.each([
    ['duplicate binding', [defaultArtifact(), unknownArtifact('future', 'x64', 'Void-Code-future-x64.exe'), unknownArtifact('future', 'x64', 'Void-Code-future-x64-alt.exe')]],
    ['duplicate filename', [defaultArtifact(), unknownArtifact('future', 'x64', 'Void-Code-future.exe'), unknownArtifact('future2', 'arm64', 'Void-Code-future.exe')]],
    ['wrong known filename', [{ ...defaultArtifact(), file: 'vc-windows-x64.exe' }]],
    ['path traversal', [defaultArtifact(), unknownArtifact('future', 'x64', '../Void-Code-future-x64.exe')]],
    ['absolute path', [defaultArtifact(), unknownArtifact('future', 'x64', '/tmp/Void-Code-future-x64.exe')]],
    ['overlong filename', [defaultArtifact(), unknownArtifact('future', 'x64', `${'a'.repeat(256)}.exe`)]],
    ['upper-case digest', [defaultArtifact(), { ...unknownArtifact(), sha256: 'A'.repeat(64) }]],
    ['too-small size', [defaultArtifact(), { ...unknownArtifact(), size: 0 }]],
    ['too-large size', [defaultArtifact(), { ...unknownArtifact(), size: 1_073_741_825 }]],
    ['unsafe size', [defaultArtifact(), { ...unknownArtifact(), size: Number.MAX_SAFE_INTEGER + 1 }]],
  ])('rejects malformed artifact: %s', async (_label, artifacts) => {
    expect((await selected(manifest({ artifacts }))).ok).toBe(false);
  });

  it('accepts a valid selected artifact alongside unique valid future targets', async () => {
    expect((await selected(manifest({ artifacts: [
      defaultArtifact(),
      unknownArtifact('future', 'x64', 'Void-Code-future-x64.exe'),
      unknownArtifact('future2', 'arm64', 'Void-Code-future2-arm64.exe'),
    ] }))).ok).toBe(true);
  });

  it('treats distinct future artifact filenames as case-sensitive', async () => {
    expect((await selected(manifest({ artifacts: [
      defaultArtifact(),
      unknownArtifact('futureone', 'x64', 'Future.exe'),
      unknownArtifact('futuretwo', 'arm64', 'future.exe'),
    ] }))).ok).toBe(true);
  });

  it('accepts 32 unique well-formed artifacts but rejects 33', async () => {
    const artifacts = [defaultArtifact(), ...Array.from({ length: 32 }, (_, index) =>
      unknownArtifact(`future${index}`, 'x64', `Void-Code-future${index}-x64.exe`),
    )];
    expect((await selected(manifest({ artifacts: artifacts.slice(0, 32) }))).ok).toBe(true);
    expect((await selected(manifest({ artifacts }))).ok).toBe(false);
  });

  it.each([1, 1_073_741_824])('accepts bounded safe artifact size %i', async (size) => {
    const artifact = { ...defaultArtifact(), size };
    expect((await selected(manifest({ artifacts: [artifact] }))).ok).toBe(true);
  });

  it('rejects generated duplicate bindings and filenames rather than selecting ambiguity', async () => {
    await fc.assert(fc.asyncProperty(
      fc.stringMatching(/^[a-z]{1,8}$/),
      fc.stringMatching(/^[A-Za-z]{1,8}\.exe$/),
      async (platform, file) => {
        const first = unknownArtifact(platform, 'x64', file);
        const alternateFile = file.replace(/\.exe$/, '-alt.exe');
        const duplicateBinding: Artifact = { ...first, file: alternateFile };
        const duplicateName: Artifact = { ...first, platform: `${platform}2`, arch: 'arm64' };
        const unique = unknownArtifact(`${platform}3`, 'arm64', alternateFile);
        expect((await selected(manifest({ artifacts: [defaultArtifact(), first, unique] }))).ok).toBe(true);
        expect((await selected(manifest({ artifacts: [defaultArtifact(), first, duplicateBinding] }))).ok).toBe(false);
        expect((await selected(manifest({ artifacts: [defaultArtifact(), first, duplicateName] }))).ok).toBe(false);
      },
    ), { numRuns: 30 });
  });

  it('does not fall back across architecture and ignores valid future targets', async () => {
    const arm = artifactFor(undefined, { platform: 'win32', arch: 'arm64', file: 'Void-Code-windows-arm64.exe' });
    expect(await selected(manifest({ artifacts: [arm] }))).toMatchObject({ ok: false, code: 'unsupported-target' });
    expect(await selected(manifest({ artifacts: [defaultArtifact(), arm] }))).toMatchObject({ ok: true, plan: { artifact: defaultArtifact() } });
  });

  it.each([
    installed({ packaged: false }),
    installed({ version: '1.0.0-dev' }),
    installed({ platform: 'linux', arch: 'x64' }),
  ])('rejects a non-release installed build', async (build) => {
    const result = await selected(manifest(), build);
    expect(result).toMatchObject({ ok: false });
  });

  it.each([
    ['same installed and candidate', '1.2.3', '1.2.3', false],
    ['candidate lower', '1.2.4', '1.2.3', false],
    ['major rollover', '1.9.9', '2.0.0', true],
    ['minor 9 to 10', '1.9.9', '1.10.0', true],
    ['patch lower', '1.2.4', '1.2.3', false],
  ])('orders versions for %s', async (_label, installedVersion, candidateVersion, accepted) => {
    const result = await selected(manifest({ version: candidateVersion, tag: `v${candidateVersion}` }), installed({ version: installedVersion }));
    expect(result.ok).toBe(accepted);
  });

  it.each(['1.2', '1.2.3.4', '-1.2.3', ' 1.2.3', '1.2.3 ', '1..3', '1.02.3', '9007199254740992.0.0'])('rejects invalid or unsafe semver %s', async (version) => {
    expect((await selected(manifest({ version, tag: `v${version}` }))).ok).toBe(false);
  });
});

import { existsSync } from 'node:fs';
import { describe, expect, it, vi } from 'vitest';
import {
  artifactFor,
  deferred,
  envelope,
  installed,
  keyring,
  manifest,
  payloadBytes,
  testPublicKey,
  ticks,
  type ControllerDependencies,
  type ControllerModule,
  type StageHandle,
  type TrustModule,
  type UpdateSnapshot,
} from './desktop-update-fixtures.ts';

async function trust(): Promise<TrustModule> {
  const file = new URL('../src/main/desktop-update-trust.ts', import.meta.url);
  expect(existsSync(file), 'desktop update trust is not implemented').toBe(true);
  return import(file.href) as Promise<TrustModule>;
}

async function controllerModule(): Promise<ControllerModule> {
  const file = new URL('../src/main/desktop-update-controller.ts', import.meta.url);
  expect(existsSync(file), 'desktop update controller is not implemented').toBe(true);
  return import(file.href) as Promise<ControllerModule>;
}

describe('desktop update hardening boundaries', () => {
  it('accepts a 64-character keyId but rejects an otherwise valid 65-character keyId', async () => {
    const verifier = await trust();
    const keyId64 = 'k'.repeat(64);
    const keyId65 = 'k'.repeat(65);
    const valid = verifier.verifyDesktopUpdateEnvelope(envelope(payloadBytes(), { keyId: keyId64 }), { [keyId64]: testPublicKey });
    const overlong = verifier.verifyDesktopUpdateEnvelope(envelope(payloadBytes(), { keyId: keyId65 }), { [keyId65]: testPublicKey });

    expect(valid.ok).toBe(true);
    expect(overlong.ok).toBe(false);
  });

  it('accepts an ignored future filename of 128 ASCII characters but rejects 129', async () => {
    const verifier = await trust();
    const filename128 = `f${'a'.repeat(127)}`;
    const filename129 = `f${'a'.repeat(128)}`;
    const futureArtifact = (file: string) => artifactFor(undefined, { platform: 'future', arch: 'x64', file });
    const accepted = verifier.parseAndSelectDesktopRelease(
      payloadBytes(manifest({ artifacts: [artifactFor(), futureArtifact(filename128)] })),
      installed(),
    );
    const rejected = verifier.parseAndSelectDesktopRelease(
      payloadBytes(manifest({ artifacts: [artifactFor(), futureArtifact(filename129)] })),
      installed(),
    );

    expect(accepted.ok).toBe(true);
    expect(rejected.ok).toBe(false);
  });

  // Ordering requirement from the approved specification, not an existing production incident.
  it('rejects an invalid signature before attempting malformed payload decoding or parsing', async () => {
    const verifier = await trust();
    const invalidSignature = new Uint8Array(64);
    const invalidWithWellFormedPayload = verifier.verifyDesktopUpdateEnvelope(envelope(payloadBytes(), { signature: invalidSignature }), keyring);
    const invalidWithMalformedPayload = verifier.verifyDesktopUpdateEnvelope(envelope(new Uint8Array([0xff]), { signature: invalidSignature }), keyring);
    const signedMalformedPayload = verifier.verifyDesktopUpdateEnvelope(envelope(new Uint8Array([0xff])), keyring);

    expect(invalidWithWellFormedPayload.ok).toBe(false);
    expect(invalidWithMalformedPayload.ok).toBe(false);
    if (!invalidWithWellFormedPayload.ok && !invalidWithMalformedPayload.ok) {
      expect(invalidWithWellFormedPayload.code).toBe('invalid-signature');
      expect(invalidWithMalformedPayload.code).toBe('invalid-signature');
    }
    expect(signedMalformedPayload.ok).toBe(false);
    if (!signedMalformedPayload.ok) expect(signedMalformedPayload.code).not.toBe('invalid-signature');
  });
});

describe('desktop update check reentrancy', () => {
  it('shares a synchronous checking-snapshot reentrant check until metadata resolves', async () => {
    const metadata = deferred<Uint8Array>();
    const snapshots: UpdateSnapshot[] = [];
    const stage: StageHandle = { id: 'unused-stage' };
    let inner!: Promise<void>;
    let reentered = false;
    const dependencies: ControllerDependencies = {
      installed: installed(),
      keyring,
      loadFloor: vi.fn<ControllerDependencies['loadFloor']>(async () => undefined),
      saveFloor: vi.fn<ControllerDependencies['saveFloor']>(async () => undefined),
      fetchMetadata: vi.fn<ControllerDependencies['fetchMetadata']>(() => metadata.promise),
      download: vi.fn<ControllerDependencies['download']>(async () => stage),
      verifyArtifact: vi.fn<ControllerDependencies['verifyArtifact']>(async () => true),
      showNativeInstallDialog: vi.fn<ControllerDependencies['showNativeInstallDialog']>(async () => false),
      prepare: vi.fn<ControllerDependencies['prepare']>(async () => undefined),
      reverify: vi.fn<ControllerDependencies['reverify']>(async () => true),
      handoff: vi.fn<ControllerDependencies['handoff']>(async () => undefined),
      onSnapshot: vi.fn<ControllerDependencies['onSnapshot']>((snapshot) => {
        snapshots.push(snapshot);
        if (snapshot.state === 'checking' && !reentered) {
          reentered = true;
          inner = controller.check();
        }
      }),
    };
    const controller = (await controllerModule()).createDesktopUpdateController(dependencies);

    let outerSettled = false;
    let innerSettled = false;
    const outer = controller.check().then(() => { outerSettled = true; });
    await ticks();
    const observedInner = inner.then(() => { innerSettled = true; });

    expect(reentered).toBe(true);
    expect(dependencies.fetchMetadata).toHaveBeenCalledOnce();
    expect(outerSettled).toBe(false);
    expect(innerSettled).toBe(false);

    metadata.resolve(envelope(payloadBytes(manifest())));
    await Promise.all([outer, observedInner]);

    expect(controller.snapshot().state).toBe('available');
    expect(snapshots.some((snapshot) => snapshot.state === 'available')).toBe(true);
  });
});

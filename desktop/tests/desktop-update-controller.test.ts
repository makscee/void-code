import { existsSync } from 'node:fs';
import fc from 'fast-check';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  artifactBytes,
  artifactFor,
  deferred,
  envelope,
  installed,
  keyring,
  manifest,
  payloadBytes,
  sha256,
  ticks,
  type ControllerDependencies,
  type ControllerModule,
  type DesktopUpdateController,
  type ReplayFloor,
  type StageHandle,
  type UpdatePlan,
  type UpdateSnapshot,
} from './desktop-update-fixtures.ts';

async function controllerModule(): Promise<ControllerModule> {
  const file = new URL('../src/main/desktop-update-controller.ts', import.meta.url);
  expect(existsSync(file), 'desktop update controller is not implemented').toBe(true);
  return import(file.href) as Promise<ControllerModule>;
}

type SeamOverrides = Partial<Omit<ControllerDependencies, 'installed' | 'keyring' | 'metadataTimeoutMs'>>;
type Rig = ReturnType<typeof rig>;
function rig(overrides: Partial<ControllerDependencies> = {}) {
  const snapshots: UpdateSnapshot[] = [];
  const bytes = artifactBytes();
  const stage: StageHandle = { id: 'stage-1' };
  const stages = new Map<string, Uint8Array>([[stage.id, bytes]]);
  const floor: { value?: ReplayFloor } = {};
  const artifact = artifactFor(bytes);
  const planMatchesStage = (plan: UpdatePlan, handle: StageHandle): boolean => {
    const staged = stages.get(handle.id);
    return staged !== undefined && plan.artifact.size === staged.byteLength && plan.artifact.sha256 === sha256(staged);
  };
  const seams = {
    loadFloor: vi.fn(async (): Promise<ReplayFloor | undefined> => floor.value),
    saveFloor: vi.fn(async (next: ReplayFloor): Promise<void> => { floor.value = next; }),
    fetchMetadata: vi.fn<ControllerDependencies['fetchMetadata']>(async (): Promise<Uint8Array> => envelope(payloadBytes(manifest({ artifacts: [artifact] })))),
    download: vi.fn<ControllerDependencies['download']>(async (...args): Promise<StageHandle> => {
      args[2](0.5);
      return stage;
    }),
    verifyArtifact: vi.fn(async (plan: UpdatePlan, handle: StageHandle): Promise<boolean> => planMatchesStage(plan, handle)),
    showNativeInstallDialog: vi.fn<ControllerDependencies['showNativeInstallDialog']>(async (): Promise<boolean> => true),
    prepare: vi.fn<ControllerDependencies['prepare']>(async (): Promise<void> => undefined),
    reverify: vi.fn(async (plan: UpdatePlan, handle: StageHandle): Promise<boolean> => planMatchesStage(plan, handle)),
    handoff: vi.fn<ControllerDependencies['handoff']>(async (): Promise<void> => undefined),
    onSnapshot: vi.fn((snapshot: UpdateSnapshot): void => { snapshots.push(snapshot); }),
  };
  const { installed: installedOverride, keyring: keyringOverride, metadataTimeoutMs, ...seamOverrides } = overrides;
  Object.assign(seams, seamOverrides as SeamOverrides);
  // `seams` is the same object of callable references passed to the SUT: never a stale copy.
  const dependencies: ControllerDependencies = {
    installed: installedOverride ?? installed(),
    keyring: keyringOverride ?? keyring,
    ...seams,
    ...(metadataTimeoutMs === undefined ? {} : { metadataTimeoutMs }),
  };
  return {
    dependencies,
    seams,
    snapshots,
    bytes,
    stage,
    stages,
    floor,
    artifact,
    create: async (): Promise<DesktopUpdateController> => (await controllerModule()).createDesktopUpdateController(dependencies),
  };
}
async function available(r: Rig): Promise<DesktopUpdateController> {
  const controller = await r.create();
  await controller.check();
  expect(controller.snapshot()).toMatchObject({ state: 'available', version: '1.2.3' });
  return controller;
}
async function ready(r: Rig): Promise<DesktopUpdateController> {
  const controller = await available(r);
  await controller.download();
  expect(controller.snapshot().state).toBe('ready');
  return controller;
}
function expectNoInstallSeams(r: Rig): void {
  expect(r.seams.showNativeInstallDialog).not.toHaveBeenCalled();
  expect(r.seams.prepare).not.toHaveBeenCalled();
  expect(r.seams.handoff).not.toHaveBeenCalled();
}

describe('desktop update replay floor', () => {
  it('persists the highest verified version and digest before the available snapshot', async () => {
    const r = rig();
    const controller = await r.create();
    await controller.check();

    const saveOrder = r.seams.saveFloor.mock.invocationCallOrder[0]!;
    const availableCall = r.seams.onSnapshot.mock.calls.findIndex(([value]) => value.state === 'available');
    expect(r.floor.value).toEqual({ version: '1.2.3', payloadDigest: sha256(payloadBytes(manifest({ artifacts: [r.artifact] }))) });
    expect(saveOrder).toBeLessThan(r.seams.onSnapshot.mock.invocationCallOrder[availableCall]!);
  });

  it.each([
    ['newer persisted floor', { version: '2.0.0', payloadDigest: 'b'.repeat(64) }],
    ['same-version equivocation', { version: '1.2.3', payloadDigest: 'b'.repeat(64) }],
  ])('fails closed on %s without making download available', async (_label, stored) => {
    const r = rig({ loadFloor: vi.fn(async () => stored) });
    const controller = await r.create();
    await controller.check();

    expect(controller.snapshot().state).toMatch(/unavailable|failed/);
    expect(r.seams.download).not.toHaveBeenCalled();
    expect(r.snapshots).not.toContainEqual(expect.objectContaining({ state: 'available' }));
  });

  it('permits the exact same persisted version and digest', async () => {
    const bytes = artifactBytes();
    const descriptor = artifactFor(bytes);
    const digest = sha256(payloadBytes(manifest({ artifacts: [descriptor] })));
    const r = rig({ loadFloor: vi.fn(async () => ({ version: '1.2.3', payloadDigest: digest })) });
    const controller = await r.create();
    await controller.check();
    expect(controller.snapshot().state).toBe('available');
  });

  it('accepts an absent or older valid floor, then rejects corruption of that otherwise valid value', async () => {
    const absent = rig({ loadFloor: vi.fn(async () => undefined) });
    const withoutFloor = await absent.create();
    await withoutFloor.check();
    expect(withoutFloor.snapshot().state).toBe('available');

    const validOlderFloor = (r: Rig): ReplayFloor => ({
      version: '1.1.0',
      payloadDigest: sha256(payloadBytes(manifest({ version: '1.1.0', artifacts: [r.artifact] }))),
    });
    const clean = rig();
    const accepted = await clean.create();
    clean.seams.loadFloor.mockResolvedValue(validOlderFloor(clean));
    await accepted.check();
    expect(accepted.snapshot().state).toBe('available');

    for (const [label, corrupt] of [
      ['extra key', (floor: ReplayFloor) => ({ ...floor, extra: true })],
      ['sequence', (floor: ReplayFloor) => ({ ...floor, sequence: 1 })],
    ] as const) {
      const r = rig();
      r.seams.loadFloor.mockResolvedValue(corrupt(validOlderFloor(r)) as ReplayFloor);
      const controller = await r.create();
      await controller.check();
      expect(controller.snapshot().state, label).toMatch(/unavailable|failed/);
      expect(r.snapshots, label).not.toContainEqual(expect.objectContaining({ state: 'available' }));
    }
  });

  it.each([
    ['malformed version', { version: '1.2', payloadDigest: 'a'.repeat(64) }],
    ['missing digest', { version: '1.2.3' }],
    ['bad digest', { version: '1.2.3', payloadDigest: 'not-a-digest' }],
    ['wrong primitive', { version: 123, payloadDigest: 'a'.repeat(64) }],
    ['null', null],
    ['array', []],
  ])('validates and rejects corrupt stored floor value: %s', async (_label, value) => {
    const r = rig({ loadFloor: vi.fn(async () => value as unknown as ReplayFloor) });
    const controller = await r.create();
    await controller.check();

    expect(controller.snapshot().state).toMatch(/unavailable|failed/);
    expect(r.snapshots).not.toContainEqual(expect.objectContaining({ state: 'available' }));
  });

  it.each(['load failure', 'save failure'])('fails closed but permits manual retry after %s', async (kind) => {
    const r = rig(kind === 'load failure'
      ? { loadFloor: vi.fn(async () => { throw new Error('corrupt-floor'); }) }
      : { saveFloor: vi.fn(async () => { throw new Error('disk-full'); }) });
    const controller = await r.create();
    await controller.check();
    expect(controller.snapshot().state).toMatch(/unavailable|failed/);

    await controller.check({ manual: true });
    expect(r.seams.fetchMetadata).toHaveBeenCalledTimes(2);
  });
});

describe('desktop update consent and opaque staged artifact FSM', () => {
  beforeEach(() => { vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); });

  it.each([
    installed({ packaged: false }),
    installed({ version: '1.0.0-dev' }),
    installed({ platform: 'linux', arch: 'x64' }),
  ])('does nothing at controller boundary for unsupported installed build', async (build) => {
    const r = rig({ installed: build });
    const controller = await r.create();
    await controller.check();
    await controller.download();
    await controller.requestInstall();

    expect(r.seams.fetchMetadata).not.toHaveBeenCalled();
    expect(r.seams.download).not.toHaveBeenCalled();
    expectNoInstallSeams(r);
  });

  it('check only fetches metadata: it cannot download, ask consent, prepare, or handoff', async () => {
    const r = rig();
    await available(r);
    expect(r.seams.download).not.toHaveBeenCalled();
    expectNoInstallSeams(r);
  });

  it('does not download or install before availability, after failed check, or after dispose', async () => {
    const r = rig({ fetchMetadata: vi.fn(async () => new Uint8Array([0xff])) });
    const controller = await r.create();
    await controller.download();
    await controller.check();
    await controller.download();
    controller.dispose();
    await controller.requestInstall();
    expect(r.seams.download).not.toHaveBeenCalled();
    expectNoInstallSeams(r);
    expect(r.snapshots).not.toContainEqual(expect.objectContaining({ state: 'available' }));
    expect(r.snapshots).not.toContainEqual(expect.objectContaining({ state: 'ready' }));
  });

  it('downloads to ready but does not imply consent, prepare, or handoff', async () => {
    const r = rig();
    await ready(r);
    expectNoInstallSeams(r);
  });

  it('does not install a ready stage on normal app quit', async () => {
    const r = rig();
    const controller = await ready(r);
    // A ready normal quit must not install.
    controller.dispose();
    await controller.requestInstall();
    expectNoInstallSeams(r);
  });

  it('uses a bounded opaque stage handle and verifies its actual staged bytes before ready', async () => {
    const r = rig();
    await ready(r);
    const handle = r.seams.verifyArtifact.mock.calls[0]![1];
    expect(handle).toEqual(r.stage);
    expect(handle).not.toHaveProperty('bytes');
    expect(r.seams.verifyArtifact).toHaveBeenCalledWith(expect.objectContaining({ artifact: r.artifact }), r.stage);
  });

  it('fails download verification without emitting ready or exposing install seams', async () => {
    const r = rig({ verifyArtifact: vi.fn(async () => false) });
    const controller = await available(r);
    await controller.download();
    expect(controller.snapshot().state).toBe('failed');
    expect(r.snapshots).not.toContainEqual(expect.objectContaining({ state: 'ready' }));
    expectNoInstallSeams(r);
  });

  it('retries a rejected explicit download directly without refetching metadata', async () => {
    const r = rig();
    r.seams.download.mockRejectedValueOnce(new Error('transient download failure'));
    const controller = await available(r);

    await controller.download();
    expect(controller.snapshot().state).toBe('failed');
    await controller.download();

    expect(controller.snapshot().state).toBe('ready');
    expect(r.seams.download).toHaveBeenCalledTimes(2);
    expect(r.seams.fetchMetadata).toHaveBeenCalledOnce();
    expectNoInstallSeams(r);
  });

  it('retries an initially failed artifact verification directly with the pinned candidate', async () => {
    const r = rig();
    r.seams.verifyArtifact.mockResolvedValueOnce(false);
    const controller = await available(r);

    await controller.download();
    expect(controller.snapshot().state).toBe('failed');
    await controller.download();

    expect(controller.snapshot().state).toBe('ready');
    expect(r.seams.download).toHaveBeenCalledTimes(2);
    expect(r.seams.verifyArtifact).toHaveBeenCalledTimes(2);
    expect(r.seams.fetchMetadata).toHaveBeenCalledOnce();
    expectNoInstallSeams(r);
  });

  it('preserves the signed envelope for every downstream main-process callback', async () => {
    type AuthorizedUpdatePlan = UpdatePlan & { envelopeBytes?: Uint8Array };
    const originalEnvelope = envelope(payloadBytes(manifest()));
    const transportEnvelope = originalEnvelope.slice();
    const received: Uint8Array[] = [];
    const capture = (plan: UpdatePlan): Uint8Array => {
      const { envelopeBytes } = plan as AuthorizedUpdatePlan;
      expect(envelopeBytes).toBeInstanceOf(Uint8Array);
      expect([...envelopeBytes!]).toEqual([...originalEnvelope]);
      received.push(envelopeBytes!.slice());
      return envelopeBytes!;
    };
    const r = rig({
      fetchMetadata: vi.fn(async () => transportEnvelope),
      download: vi.fn(async (plan, _signal, progress) => {
        const envelopeBytes = capture(plan);
        envelopeBytes.fill(0);
        progress(0.5);
        return { id: 'stage-1' };
      }),
      verifyArtifact: vi.fn(async (plan) => { capture(plan); return true; }),
      showNativeInstallDialog: vi.fn(async (plan) => { capture(plan); return true; }),
      prepare: vi.fn(async (plan) => { capture(plan); }),
      reverify: vi.fn(async (plan) => { capture(plan); return true; }),
      handoff: vi.fn(async (plan) => { capture(plan); }),
    });
    const controller = await r.create();

    await controller.check();
    transportEnvelope.fill(0);
    await controller.download();
    await controller.requestInstall();

    expect(controller.snapshot().state).toBe('installing');
    expect(r.seams.download).toHaveBeenCalledOnce();
    expect(r.seams.verifyArtifact).toHaveBeenCalledOnce();
    expect(r.seams.showNativeInstallDialog).toHaveBeenCalledOnce();
    expect(r.seams.prepare).toHaveBeenCalledOnce();
    expect(r.seams.reverify).toHaveBeenCalledOnce();
    expect(r.seams.handoff).toHaveBeenCalledOnce();
    expect(received).toHaveLength(6);
    for (const envelopeBytes of received) expect([...envelopeBytes]).toEqual([...originalEnvelope]);
  });

  it('cancels an abort-ignoring download and settles its public promise before transport resolves', async () => {
    const pending = deferred<StageHandle>();
    let aborted = false;
    const r = rig({ download: vi.fn((_plan, signal) => {
      signal.addEventListener('abort', () => { aborted = true; });
      return pending.promise;
    }) });
    const controller = await available(r);
    let complete = false;
    const work = controller.download().then(() => { complete = true; }, () => { complete = true; });
    await ticks();
    controller.cancelDownload();
    await vi.advanceTimersByTimeAsync(0);
    await ticks();

    expect(aborted).toBe(true);
    expect(complete).toBe(true);
    expect(controller.snapshot().state).toBe('available');
    expect(r.seams.handoff).not.toHaveBeenCalled();
    pending.resolve(r.stage);
    await work;
  });

  it('has no public confirmInstall bypass and waits for the native dialog', async () => {
    const consent = deferred<boolean>();
    const r = rig({ showNativeInstallDialog: vi.fn(() => consent.promise) });
    const controller = await ready(r);
    expect((controller as unknown as Record<string, unknown>).confirmInstall).toBeUndefined();

    const request = controller.requestInstall();
    await ticks();
    expect(r.seams.showNativeInstallDialog).toHaveBeenCalledOnce();
    expect(r.seams.prepare).not.toHaveBeenCalled();
    consent.resolve(true);
    await request;
    expect(r.seams.prepare).toHaveBeenCalledOnce();
  });

  it('native consent rejection remains ready and leaves staging/helper inert', async () => {
    const r = rig({ showNativeInstallDialog: vi.fn(async () => false) });
    const controller = await ready(r);
    await controller.requestInstall();
    expect(controller.snapshot().state).toBe('ready');
    expect(r.seams.prepare).not.toHaveBeenCalled();
    expect(r.seams.handoff).not.toHaveBeenCalled();
  });

  it('orders native consent, non-destructive prepare, reverify, and handoff on one pinned stage', async () => {
    const calls: string[] = [];
    const r = rig({
      showNativeInstallDialog: vi.fn(async () => { calls.push('consent'); return true; }),
      prepare: vi.fn(async (_plan, handle) => { calls.push(`prepare:${handle.id}`); }),
      reverify: vi.fn(async (_plan, handle) => { calls.push(`reverify:${handle.id}`); return true; }),
      handoff: vi.fn(async (_plan, handle) => { calls.push(`handoff:${handle.id}`); }),
    });
    const controller = await ready(r);
    await controller.requestInstall();
    expect(calls).toEqual(['consent', 'prepare:stage-1', 'reverify:stage-1', 'handoff:stage-1']);
    expect(controller.snapshot().state).toBe('installing');
  });

  it('fails reverify when the staged bytes disappear (unsigned stage) before handoff', async () => {
    const r = rig();
    const controller = await ready(r);
    r.stages.delete(r.stage.id);
    await controller.requestInstall();
    expect(controller.snapshot().state).toBe('failed');
    expect(r.seams.handoff).not.toHaveBeenCalled();
  });

  it('fails reverify when staged bytes are tampered after initial verification', async () => {
    const r = rig();
    const controller = await ready(r);
    r.stages.set(r.stage.id, new Uint8Array([9, 9, 9, 9]));
    await controller.requestInstall();
    expect(controller.snapshot().state).toBe('failed');
    expect(r.seams.handoff).not.toHaveBeenCalled();
  });

  it.each(['prepare', 'handoff'])('fails safely when %s throws', async (seam) => {
    const r = rig(seam === 'prepare'
      ? { prepare: vi.fn(async () => { throw new Error('prepare failed'); }) }
      : { handoff: vi.fn(async () => { throw new Error('handoff failed'); }) });
    const controller = await ready(r);
    await controller.requestInstall();
    expect(controller.snapshot().state).toBe('failed');
  });

  it('coalesces double install request into one native dialog and one handoff', async () => {
    const consent = deferred<boolean>();
    const r = rig({ showNativeInstallDialog: vi.fn(() => consent.promise) });
    const controller = await ready(r);
    const first = controller.requestInstall();
    const second = controller.requestInstall();
    await ticks();
    expect(r.seams.showNativeInstallDialog).toHaveBeenCalledOnce();
    consent.resolve(true);
    await Promise.all([first, second]);
    expect(r.seams.handoff).toHaveBeenCalledOnce();
  });

  it('snapshot never exposes the plan URL/path/key or staged bytes', async () => {
    const r = rig();
    const controller = await ready(r);
    const snapshot = controller.snapshot() as Record<string, unknown>;
    for (const key of ['plan', 'url', 'path', 'keyId', 'payloadBytes', 'bytes', 'stage']) {
      expect(snapshot).not.toHaveProperty(key);
    }
  });
});

describe('desktop update single-flight and generation ownership', () => {
  beforeEach(() => { vi.useFakeTimers(); });
  afterEach(() => { vi.useRealTimers(); });

  it('coalesces double check and double download using dependencies configured before construction', async () => {
    const metadata = deferred<Uint8Array>();
    const downloads = deferred<StageHandle>();
    const r = rig({
      fetchMetadata: vi.fn(() => metadata.promise),
      download: vi.fn(() => downloads.promise),
    });
    const controller = await r.create();
    const firstCheck = controller.check();
    const secondCheck = controller.check();
    expect(r.seams.fetchMetadata).toHaveBeenCalledOnce();
    metadata.resolve(envelope(payloadBytes(manifest({ artifacts: [r.artifact] }))));
    await Promise.all([firstCheck, secondCheck]);

    const firstDownload = controller.download();
    const secondDownload = controller.download();
    expect(r.seams.download).toHaveBeenCalledOnce();
    downloads.resolve(r.stage);
    await Promise.all([firstDownload, secondDownload]);
  });

  it('makes late progress, resolution, and rejection inert after cancel/dispose', async () => {
    const resolvePending = deferred<StageHandle>();
    let progress!: (value: number) => void;
    const r = rig({ download: vi.fn((_plan, _signal, report) => { progress = report; return resolvePending.promise; }) });
    const controller = await available(r);
    const work = controller.download();
    await ticks();
    controller.cancelDownload();
    controller.dispose();
    progress(0.9);
    resolvePending.resolve(r.stage);
    await work;
    expect(controller.snapshot().state).toBe('available');
    expect(r.snapshots.at(-1)).not.toMatchObject({ state: 'ready', progress: 0.9 });

    const rejectPending = deferred<StageHandle>();
    const rejected = rig({ download: vi.fn(() => rejectPending.promise) });
    const retry = await available(rejected);
    const rejectedWork = retry.download();
    await ticks();
    retry.cancelDownload();
    rejectPending.reject(new Error('late transport rejection'));
    await rejectedWork;
    expect(retry.snapshot().state).toBe('available');
  });

  it('retry after cancellation owns a new generation and ignores old completion', async () => {
    const first = deferred<StageHandle>();
    const second = deferred<StageHandle>();
    const r = rig({ download: vi.fn().mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise) });
    const controller = await available(r);
    const old = controller.download();
    await ticks();
    controller.cancelDownload();
    const fresh = controller.download();
    await ticks();
    expect(r.seams.download).toHaveBeenCalledTimes(2);
    first.resolve(r.stage);
    await old;
    expect(controller.snapshot().state).toBe('downloading');
    second.resolve(r.stage);
    await fresh;
    expect(controller.snapshot().state).toBe('ready');
  });

  it('does not start a check during download or erase its pinned candidate', async () => {
    const pending = deferred<StageHandle>();
    const r = rig({ download: vi.fn(() => pending.promise) });
    const controller = await available(r);
    const work = controller.download();
    await ticks();
    await controller.check();
    expect(r.seams.fetchMetadata).toHaveBeenCalledOnce();
    pending.resolve(r.stage);
    await work;
    expect(controller.snapshot().state).toBe('ready');
  });

  it('times out hung abort-ignoring metadata and ignores its late result', async () => {
    const pending = deferred<Uint8Array>();
    const r = rig({ metadataTimeoutMs: 10, fetchMetadata: vi.fn(() => pending.promise) });
    const controller = await r.create();
    const work = controller.check();
    await vi.advanceTimersByTimeAsync(10);
    await work;
    expect(controller.snapshot().state).toMatch(/unavailable|failed/);
    pending.resolve(envelope());
    await ticks();
    expect(controller.snapshot().state).toMatch(/unavailable|failed/);
  });

  it.each([
    ['omitted metadataTimeoutMs', undefined],
    ['metadataTimeoutMs=60000', 60_000],
  ])('enforces the 10-second metadata deadline when %s', async (_label, metadataTimeoutMs) => {
    const pending = deferred<Uint8Array>();
    let aborted = false;
    const r = rig({
      metadataTimeoutMs,
      fetchMetadata: vi.fn((signal) => {
        signal.addEventListener('abort', () => { aborted = true; });
        return pending.promise;
      }),
    });
    const controller = await r.create();
    let complete = false;
    const work = controller.check().then(() => { complete = true; }, () => { complete = true; });
    await ticks();

    await vi.advanceTimersByTimeAsync(9_999);
    await ticks();
    expect(complete).toBe(false);

    await vi.advanceTimersByTimeAsync(1);
    await ticks();
    expect(complete).toBe(true);
    expect(aborted).toBe(true);
    expect(controller.snapshot()).toMatchObject({ state: 'unavailable', error: 'metadata-timeout' });

    pending.resolve(envelope());
    await ticks();
    expect(controller.snapshot()).toMatchObject({ state: 'unavailable', error: 'metadata-timeout' });
    await work;
  });

  it('keeps generated concurrent operations to one authorized live download and never hands off before accepted native consent', async () => {
    await fc.assert(fc.asyncProperty(
      fc.array(fc.constantFrom('download', 'install', 'accept', 'reject', 'settle', 'cancel', 'dispose', 'check'), { minLength: 4, maxLength: 20 }),
      async (events) => {
        const pending: Array<ReturnType<typeof deferred<StageHandle>>> = [];
        const dialogAnswers: boolean[] = [];
        let authorized = 0;
        let maximumAuthorized = 0;
        let acceptedDialogs = 0;
        const r = rig({
          download: vi.fn((_plan, signal) => {
            authorized += 1;
            maximumAuthorized = Math.max(maximumAuthorized, authorized);
            let deauthorized = false;
            const deauthorize = () => {
              if (!deauthorized) {
                deauthorized = true;
                authorized -= 1;
              }
            };
            signal.addEventListener('abort', deauthorize, { once: true });
            const next = deferred<StageHandle>();
            pending.push(next);
            return next.promise.finally(deauthorize);
          }),
          showNativeInstallDialog: vi.fn(async () => {
            const answer = dialogAnswers.shift() ?? false;
            if (answer) acceptedDialogs += 1;
            return answer;
          }),
          handoff: vi.fn(async () => { expect(acceptedDialogs).toBeGreaterThan(0); }),
        });
        const controller = await available(r);
        const calls: Promise<void>[] = [];
        for (const event of events) {
          if (event === 'download') calls.push(controller.download());
          if (event === 'install') calls.push(controller.requestInstall());
          if (event === 'accept') dialogAnswers.push(true);
          if (event === 'reject') dialogAnswers.push(false);
          if (event === 'settle') pending.shift()?.resolve(r.stage);
          if (event === 'cancel') controller.cancelDownload();
          if (event === 'dispose') controller.dispose();
          if (event === 'check') calls.push(controller.check());
          await ticks();
          expect(authorized).toBeLessThanOrEqual(1);
        }
        for (const request of pending) request.resolve(r.stage);
        await Promise.all(calls);
        expect(maximumAuthorized).toBeLessThanOrEqual(1);
      },
    ), { numRuns: 30 });
  });
});

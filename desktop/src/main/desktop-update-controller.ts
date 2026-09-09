import {
  compareStableDesktopVersions,
  isSupportedDesktopInstalledBuild,
  parseAndSelectDesktopRelease,
  parseStableDesktopVersion,
  verifyDesktopUpdateEnvelope,
} from './desktop-update-trust';
import type {
  DesktopInstalledBuild,
  DesktopUpdateArtifact,
  DesktopUpdatePlan,
} from './desktop-update-trust';

export type ReplayFloor = { version: string; payloadDigest: string };
export type StageHandle = { readonly id: string };
export type UpdateState =
  | 'idle'
  | 'checking'
  | 'up-to-date'
  | 'available'
  | 'unavailable'
  | 'unsupported'
  | 'downloading'
  | 'verifying'
  | 'ready'
  | 'confirming'
  | 'preparing'
  | 'installing'
  | 'failed';
export type UpdateSnapshot = {
  state: UpdateState;
  version?: string;
  progress?: number;
  error?: string;
};

export type DesktopUpdateControllerDependencies = {
  installed: DesktopInstalledBuild;
  keyring: Readonly<Record<string, string | Uint8Array>>;
  loadFloor(): Promise<ReplayFloor | undefined>;
  saveFloor(floor: ReplayFloor): Promise<void>;
  fetchMetadata(signal: AbortSignal): Promise<Uint8Array>;
  download(plan: DesktopUpdatePlan, signal: AbortSignal, progress: (fraction: number) => void): Promise<StageHandle>;
  verifyArtifact(plan: DesktopUpdatePlan, stage: StageHandle): Promise<boolean>;
  showNativeInstallDialog(plan: DesktopUpdatePlan): Promise<boolean>;
  prepare(plan: DesktopUpdatePlan, stage: StageHandle): Promise<void>;
  reverify(plan: DesktopUpdatePlan, stage: StageHandle): Promise<boolean>;
  handoff(plan: DesktopUpdatePlan, stage: StageHandle): Promise<void>;
  onSnapshot(snapshot: UpdateSnapshot): void;
  metadataTimeoutMs?: number;
};

export type DesktopUpdateController = {
  check(options?: { manual?: boolean }): Promise<void>;
  download(): Promise<void>;
  cancelDownload(): void;
  requestInstall(): Promise<void>;
  dispose(): void;
  snapshot(): UpdateSnapshot;
};

type ExternalOutcome<T> =
  | { status: 'fulfilled'; value: T }
  | { status: 'rejected' }
  | { status: 'cancelled' }
  | { status: 'timeout' };

type Operation = {
  readonly generation: number;
  readonly abortController: AbortController;
  readonly cancelled: Promise<ExternalOutcome<never>>;
  cancel(): void;
};

type PinnedPlan = {
  readonly version: string;
  readonly payloadDigest: string;
  readonly artifact: Readonly<DesktopUpdateArtifact>;
  readonly payloadBytes: Uint8Array;
};

const DEFAULT_METADATA_TIMEOUT_MS = 30_000;
const MAX_METADATA_TIMEOUT_MS = 300_000;
const DIGEST_PATTERN = /^[a-f0-9]{64}$/;

function copyInstalled(installed: DesktopInstalledBuild): DesktopInstalledBuild {
  return {
    product: installed.product,
    channel: installed.channel,
    version: installed.version,
    platform: installed.platform,
    arch: installed.arch,
    packaged: installed.packaged,
  };
}

function copyKeyring(
  keyring: Readonly<Record<string, string | Uint8Array>>,
): Readonly<Record<string, string | Uint8Array>> {
  const copy: Record<string, string | Uint8Array> = Object.create(null) as Record<string, string | Uint8Array>;
  for (const key of Object.keys(keyring)) {
    const value = keyring[key];
    if (typeof value === 'string') copy[key] = value;
    else if (value instanceof Uint8Array) copy[key] = new Uint8Array(value);
  }
  return Object.freeze(copy);
}

function pinPlan(plan: DesktopUpdatePlan, authenticatedDigest: string): PinnedPlan {
  return {
    version: plan.version,
    payloadDigest: authenticatedDigest,
    artifact: Object.freeze({ ...plan.artifact }),
    payloadBytes: new Uint8Array(plan.payloadBytes),
  };
}

function exposePlan(plan: PinnedPlan): DesktopUpdatePlan {
  return {
    version: plan.version,
    payloadDigest: plan.payloadDigest,
    artifact: { ...plan.artifact },
    payloadBytes: new Uint8Array(plan.payloadBytes),
  };
}

function pinStage(value: unknown): StageHandle | undefined {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return undefined;
  const record = value as Record<string, unknown>;
  if (Object.keys(record).length !== 1 || !Object.hasOwn(record, 'id')) return undefined;
  if (typeof record.id !== 'string' || record.id.length === 0 || record.id.length > 1024) return undefined;
  return Object.freeze({ id: record.id });
}

function validFloor(value: unknown): value is ReplayFloor {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const record = value as Record<string, unknown>;
  const keys = Object.keys(record);
  return keys.length === 2 && Object.hasOwn(record, 'version') && Object.hasOwn(record, 'payloadDigest') &&
    typeof record.version === 'string' && parseStableDesktopVersion(record.version) !== undefined &&
    typeof record.payloadDigest === 'string' && DIGEST_PATTERN.test(record.payloadDigest);
}

function metadataTimeout(value: number | undefined): number {
  if (value === undefined || !Number.isFinite(value) || value <= 0) return DEFAULT_METADATA_TIMEOUT_MS;
  return Math.max(1, Math.min(Math.floor(value), MAX_METADATA_TIMEOUT_MS));
}

function externalOutcome<T>(invoke: () => Promise<T>): Promise<ExternalOutcome<T>> {
  try {
    return Promise.resolve(invoke()).then(
      (value): ExternalOutcome<T> => ({ status: 'fulfilled', value }),
      (): ExternalOutcome<T> => ({ status: 'rejected' }),
    );
  } catch {
    return Promise.resolve<ExternalOutcome<T>>({ status: 'rejected' });
  }
}

function waitForOperation<T>(operation: Operation, work: Promise<ExternalOutcome<T>>): Promise<ExternalOutcome<T>> {
  return Promise.race([work, operation.cancelled]);
}

export function createDesktopUpdateController(
  suppliedDependencies: DesktopUpdateControllerDependencies,
): DesktopUpdateController {
  const dependencies = {
    installed: copyInstalled(suppliedDependencies.installed),
    keyring: copyKeyring(suppliedDependencies.keyring),
    loadFloor: suppliedDependencies.loadFloor,
    saveFloor: suppliedDependencies.saveFloor,
    fetchMetadata: suppliedDependencies.fetchMetadata,
    download: suppliedDependencies.download,
    verifyArtifact: suppliedDependencies.verifyArtifact,
    showNativeInstallDialog: suppliedDependencies.showNativeInstallDialog,
    prepare: suppliedDependencies.prepare,
    reverify: suppliedDependencies.reverify,
    handoff: suppliedDependencies.handoff,
    onSnapshot: suppliedDependencies.onSnapshot,
    metadataTimeoutMs: metadataTimeout(suppliedDependencies.metadataTimeoutMs),
  };

  let currentSnapshot: UpdateSnapshot = { state: 'idle' };
  let candidate: PinnedPlan | undefined;
  let staged: StageHandle | undefined;
  let disposed = false;
  let generation = 0;
  let checkOperation: Operation | undefined;
  let downloadOperation: Operation | undefined;
  let installOperation: Operation | undefined;
  let checkFlight: Promise<void> | undefined;
  let downloadFlight: Promise<void> | undefined;
  let installFlight: Promise<void> | undefined;

  function newOperation(): Operation {
    let resolveCancellation!: (value: ExternalOutcome<never>) => void;
    let cancelledAlready = false;
    const cancelled = new Promise<ExternalOutcome<never>>((resolve) => {
      resolveCancellation = resolve;
    });
    const abortController = new AbortController();
    generation += 1;
    return {
      generation,
      abortController,
      cancelled,
      cancel: () => {
        if (cancelledAlready) return;
        cancelledAlready = true;
        abortController.abort();
        resolveCancellation({ status: 'cancelled' });
      },
    };
  }

  function owns(operation: Operation): boolean {
    return !disposed && operation.generation === generation;
  }

  function publish(next: UpdateSnapshot): void {
    if (disposed) return;
    currentSnapshot = { ...next };
    try {
      dependencies.onSnapshot({ ...currentSnapshot });
    } catch {
      // Observer failures do not change update authorization or state ownership.
    }
  }

  function fail(operation: Operation, code: string): void {
    if (!owns(operation)) return;
    staged = undefined;
    publish({ state: 'failed', error: code });
  }

  async function fetchWithTimeout(operation: Operation): Promise<ExternalOutcome<Uint8Array>> {
    const transport = externalOutcome(() => dependencies.fetchMetadata(operation.abortController.signal));
    let timer: ReturnType<typeof setTimeout> | undefined;
    const timeout = new Promise<ExternalOutcome<Uint8Array>>((resolve) => {
      timer = setTimeout(() => {
        operation.abortController.abort();
        resolve({ status: 'timeout' });
      }, dependencies.metadataTimeoutMs);
    });
    try {
      return await Promise.race([transport, operation.cancelled, timeout]);
    } finally {
      if (timer !== undefined) clearTimeout(timer);
    }
  }

  function selectionFailureState(code: string): UpdateSnapshot {
    if (code === 'not-newer') return { state: 'up-to-date' };
    if (code === 'unsupported-target' || code === 'unsupported-installed-build') {
      return { state: 'unsupported', error: code };
    }
    return { state: 'unavailable', error: code };
  }

  async function runCheck(operation: Operation): Promise<void> {
    try {
      if (!isSupportedDesktopInstalledBuild(dependencies.installed)) {
        if (owns(operation)) publish({ state: 'unsupported' });
        return;
      }

      publish({ state: 'checking' });
      const metadata = await fetchWithTimeout(operation);
      if (!owns(operation)) return;
      if (metadata.status !== 'fulfilled') {
        publish({ state: 'unavailable', error: metadata.status === 'timeout' ? 'metadata-timeout' : 'metadata-unavailable' });
        return;
      }

      const verified = verifyDesktopUpdateEnvelope(metadata.value, dependencies.keyring);
      if (!owns(operation)) return;
      if (!verified.ok) {
        candidate = undefined;
        staged = undefined;
        publish({ state: 'unavailable', error: verified.code });
        return;
      }
      const selection = parseAndSelectDesktopRelease(verified.verified.payloadBytes, dependencies.installed);
      if (!owns(operation)) return;
      if (!selection.ok) {
        candidate = undefined;
        staged = undefined;
        publish(selectionFailureState(selection.code));
        return;
      }
      if (selection.plan.payloadDigest !== verified.verified.payloadDigest) {
        candidate = undefined;
        staged = undefined;
        publish({ state: 'unavailable', error: 'plan-identity-mismatch' });
        return;
      }
      const nextCandidate = pinPlan(selection.plan, verified.verified.payloadDigest);

      const loaded = await waitForOperation(operation, externalOutcome(() => dependencies.loadFloor()));
      if (!owns(operation)) return;
      if (loaded.status !== 'fulfilled' || (loaded.value !== undefined && !validFloor(loaded.value))) {
        candidate = undefined;
        staged = undefined;
        publish({ state: 'unavailable', error: 'replay-floor-unavailable' });
        return;
      }

      const floor = loaded.value;
      if (floor !== undefined) {
        const comparison = compareStableDesktopVersions(floor.version, nextCandidate.version);
        if (comparison === undefined || comparison > 0 ||
            (comparison === 0 && floor.payloadDigest !== nextCandidate.payloadDigest)) {
          candidate = undefined;
          staged = undefined;
          publish({ state: 'unavailable', error: 'replay-floor-rejected' });
          return;
        }
      }

      const needsSave = floor === undefined ||
        compareStableDesktopVersions(floor.version, nextCandidate.version) === -1;
      if (needsSave) {
        const saved = await waitForOperation(operation, externalOutcome(() => dependencies.saveFloor({
          version: nextCandidate.version,
          payloadDigest: nextCandidate.payloadDigest,
        })));
        if (!owns(operation)) return;
        if (saved.status !== 'fulfilled') {
          candidate = undefined;
          staged = undefined;
          publish({ state: 'unavailable', error: 'replay-floor-unavailable' });
          return;
        }
      }

      candidate = nextCandidate;
      staged = undefined;
      publish({ state: 'available', version: nextCandidate.version });
    } catch {
      if (owns(operation)) {
        candidate = undefined;
        staged = undefined;
        publish({ state: 'unavailable', error: 'metadata-unavailable' });
      }
    } finally {
      if (checkOperation === operation) {
        checkOperation = undefined;
        checkFlight = undefined;
      }
    }
  }

  function check(): Promise<void> {
    if (disposed) return Promise.resolve();
    if (checkFlight !== undefined) return checkFlight;
    if (currentSnapshot.state === 'downloading' || currentSnapshot.state === 'verifying' ||
        currentSnapshot.state === 'ready' || currentSnapshot.state === 'confirming' ||
        currentSnapshot.state === 'preparing' || currentSnapshot.state === 'installing') {
      return Promise.resolve();
    }
    if (!isSupportedDesktopInstalledBuild(dependencies.installed)) {
      publish({ state: 'unsupported' });
      return Promise.resolve();
    }
    const operation = newOperation();
    checkOperation = operation;
    const flight = runCheck(operation);
    checkFlight = flight;
    return flight;
  }

  async function runDownload(operation: Operation, plan: PinnedPlan): Promise<void> {
    try {
      publish({ state: 'downloading', version: plan.version, progress: 0 });
      const downloaded = externalOutcome(() => dependencies.download(
        exposePlan(plan),
        operation.abortController.signal,
        (fraction) => {
          if (!owns(operation) || downloadOperation !== operation || currentSnapshot.state !== 'downloading') return;
          if (!Number.isFinite(fraction)) return;
          const progress = Math.max(0, Math.min(1, fraction));
          publish({ state: 'downloading', version: plan.version, progress });
        },
      ));
      const downloadResult = await waitForOperation(operation, downloaded);
      if (!owns(operation) || downloadResult.status === 'cancelled') return;
      if (downloadResult.status !== 'fulfilled') {
        fail(operation, 'download-failed');
        return;
      }
      const stage = pinStage(downloadResult.value);
      if (stage === undefined) {
        fail(operation, 'invalid-stage-handle');
        return;
      }

      publish({ state: 'verifying', version: plan.version });
      const verification = await waitForOperation(
        operation,
        externalOutcome(() => dependencies.verifyArtifact(exposePlan(plan), stage)),
      );
      if (!owns(operation) || verification.status === 'cancelled') return;
      if (verification.status !== 'fulfilled' || verification.value !== true) {
        fail(operation, 'artifact-verification-failed');
        return;
      }

      staged = stage;
      publish({ state: 'ready', version: plan.version });
    } catch {
      fail(operation, 'download-failed');
    } finally {
      if (downloadOperation === operation) {
        downloadOperation = undefined;
        downloadFlight = undefined;
      }
    }
  }

  function download(): Promise<void> {
    if (disposed) return Promise.resolve();
    if (downloadFlight !== undefined) return downloadFlight;
    if (currentSnapshot.state !== 'available' || candidate === undefined) return Promise.resolve();
    const operation = newOperation();
    downloadOperation = operation;
    const flight = runDownload(operation, candidate);
    downloadFlight = flight;
    return flight;
  }

  function cancelDownload(): void {
    const operation = downloadOperation;
    if (disposed || operation === undefined) return;
    downloadOperation = undefined;
    downloadFlight = undefined;
    operation.cancel();
    generation += 1;
    staged = undefined;
    if (candidate !== undefined) publish({ state: 'available', version: candidate.version });
    else publish({ state: 'idle' });
  }

  async function runInstall(operation: Operation, plan: PinnedPlan, stage: StageHandle): Promise<void> {
    try {
      publish({ state: 'confirming', version: plan.version });
      const consent = await waitForOperation(
        operation,
        externalOutcome(() => dependencies.showNativeInstallDialog(exposePlan(plan))),
      );
      if (!owns(operation) || consent.status === 'cancelled') return;
      if (consent.status !== 'fulfilled') {
        fail(operation, 'native-dialog-failed');
        return;
      }
      if (consent.value !== true) {
        publish({ state: 'ready', version: plan.version });
        return;
      }

      publish({ state: 'preparing', version: plan.version });
      const prepared = await waitForOperation(
        operation,
        externalOutcome(() => dependencies.prepare(exposePlan(plan), stage)),
      );
      if (!owns(operation) || prepared.status === 'cancelled') return;
      if (prepared.status !== 'fulfilled') {
        fail(operation, 'prepare-failed');
        return;
      }

      publish({ state: 'verifying', version: plan.version });
      const verification = await waitForOperation(
        operation,
        externalOutcome(() => dependencies.reverify(exposePlan(plan), stage)),
      );
      if (!owns(operation) || verification.status === 'cancelled') return;
      if (verification.status !== 'fulfilled' || verification.value !== true) {
        fail(operation, 'artifact-reverification-failed');
        return;
      }

      publish({ state: 'installing', version: plan.version });
      const handedOff = await waitForOperation(
        operation,
        externalOutcome(() => dependencies.handoff(exposePlan(plan), stage)),
      );
      if (!owns(operation) || handedOff.status === 'cancelled') return;
      if (handedOff.status !== 'fulfilled') fail(operation, 'handoff-failed');
    } catch {
      fail(operation, 'install-failed');
    } finally {
      if (installOperation === operation) {
        installOperation = undefined;
        installFlight = undefined;
      }
    }
  }

  function requestInstall(): Promise<void> {
    if (disposed) return Promise.resolve();
    if (installFlight !== undefined) return installFlight;
    if (currentSnapshot.state !== 'ready' || candidate === undefined || staged === undefined) {
      return Promise.resolve();
    }
    const operation = newOperation();
    installOperation = operation;
    const flight = runInstall(operation, candidate, staged);
    installFlight = flight;
    return flight;
  }

  function dispose(): void {
    if (disposed) return;
    disposed = true;
    generation += 1;
    checkOperation?.cancel();
    downloadOperation?.cancel();
    installOperation?.cancel();
    checkOperation = undefined;
    downloadOperation = undefined;
    installOperation = undefined;
    checkFlight = undefined;
    downloadFlight = undefined;
    installFlight = undefined;
    if (currentSnapshot.state === 'downloading' || currentSnapshot.state === 'verifying') {
      currentSnapshot = candidate === undefined
        ? { state: 'idle' }
        : { state: 'available', version: candidate.version };
      staged = undefined;
    } else if (currentSnapshot.state === 'confirming' || currentSnapshot.state === 'preparing') {
      currentSnapshot = candidate === undefined
        ? { state: 'idle' }
        : { state: 'ready', version: candidate.version };
    }
  }

  function snapshot(): UpdateSnapshot {
    return { ...currentSnapshot };
  }

  return { check, download, cancelDownload, requestInstall, dispose, snapshot };
}

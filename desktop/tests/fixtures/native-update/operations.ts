export interface FinishTime {
  bounded<T>(promise: Promise<T>, label: string, ms: number): Promise<T>;
}

export interface FinishOperationSeams {
  time: FinishTime;
  reap: () => Promise<void>;
}

/** The owner already supplies the operation deadline; this only joins its settlement. */
export async function finishOperation<T>(done: Promise<T>, { reap }: FinishOperationSeams): Promise<T> {
  try {
    return await done;
  } catch (error) {
    try {
      await reap();
    } catch (cleanup) {
      throw new AggregateError([error, cleanup], 'operation and exact reap failed');
    }
    throw error;
  }
}

export interface SeedArchivePaths { source: string; destination: string; }
export interface SeedArchiveFs {
  sourceKind(source: string): Promise<'present' | 'missing'>;
  rename(source: string, destination: string): Promise<void>;
}

export interface SeedArchiveOptions {
  platform: string;
  now: () => number;
  sleep: (ms: number) => Promise<void>;
  /** The caller validates the exact capsule, root, and source identity once per attempt. */
  verify: () => Promise<void>;
  destinationExists: () => Promise<boolean>;
  sourceIsLink: () => Promise<boolean>;
  observe?: (event: { attempt: number; elapsedMs: number; code?: string }) => void | Promise<void>;
}

/** Archives only caller-verified fixture seeds; Windows sharing refusals get one small budget. */
export async function archiveSeed(
  { source, destination }: SeedArchivePaths,
  fs: SeedArchiveFs,
  options?: SeedArchiveOptions,
): Promise<void> {
  if (!options) throw new Error('seed archive options required');
  const started = options.now();
  if (await fs.sourceKind(source) === 'missing') {
    await options.verify();
    return;
  }
  const elapsed = () => Math.max(0, options.now() - started);
  let attempt = 0;
  let lastError: unknown;
  while (elapsed() < 5_000) {
    attempt++;
    await options.verify();
    if (await options.destinationExists()) throw new Error('seed archive destination exists');
    const link = await options.sourceIsLink();
    if (elapsed() >= 5_000) throw lastError ?? new Error('seed archive budget expired');
    try {
      await fs.rename(source, destination);
      return;
    } catch (error) {
      lastError = error;
      const code = (error as NodeJS.ErrnoException).code;
      try {
        await options.observe?.({ attempt, elapsedMs: elapsed(), code });
      } catch (observeError) {
        throw new AggregateError([error, observeError], 'seed rename and observation failed');
      }
      if (link || options.platform !== 'win32' || !['EPERM', 'EACCES', 'EBUSY'].includes(code ?? '') || elapsed() >= 5_000) {
        throw error;
      }
      const remaining = 5_000 - elapsed();
      if (remaining <= 0) throw error;
      await options.sleep(Math.min(100, remaining));
    }
  }
  throw lastError ?? new Error('seed archive budget expired');
}

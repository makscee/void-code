export interface FinishTime {
  bounded<T>(promise: Promise<T>, label: string, ms: number): Promise<T>;
}

export interface FinishOperationSeams {
  time: FinishTime;
  reap: () => Promise<void>;
}

/** Preserves the current helper completion bound; do not use for new retry policy. */
export async function finishOperation<T>(done: Promise<T>, { time, reap }: FinishOperationSeams): Promise<T> {
  try {
    return await time.bounded(done, 'helper operation completion', 45_000);
  } catch (error) {
    await reap();
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

/**
 * A deliberately one-shot setup archive: no ownership guard, retry, or fallback yet.
 * The baseline ignores options while the test-first refactor establishes the validation seam.
 */
export async function archiveSeed(
  { source, destination }: SeedArchivePaths,
  fs: SeedArchiveFs,
  _options?: SeedArchiveOptions,
): Promise<void> {
  if (await fs.sourceKind(source) === 'present') await fs.rename(source, destination);
}

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

/** A deliberately one-shot setup archive: no ownership guard, retry, or fallback yet. */
export async function archiveSeed({ source, destination }: SeedArchivePaths, fs: SeedArchiveFs): Promise<void> {
  if (await fs.sourceKind(source) === 'present') await fs.rename(source, destination);
}

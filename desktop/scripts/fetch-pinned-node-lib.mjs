import { createHash } from 'node:crypto';
import { mkdir, readFile, rename, rm } from 'node:fs/promises';
import path from 'node:path';

async function digest(file) {
  try {
    return createHash('sha256').update(await readFile(file)).digest('hex');
  } catch {
    return null;
  }
}

/**
 * Ensure the cache holds the Node archive named by the pin, and nothing else.
 *
 * Downloads through the injected `download(url, destination)` so the caller owns
 * the transport — the network never has to be reachable to exercise this.
 * Unverified bytes land on a temporary path and are only moved into place after
 * their digest matches, so a rejected download cannot leave the cache poisoned.
 */
export async function ensurePinnedNode({ pins, cacheDir, download }) {
  const archive = path.join(cacheDir, path.basename(pins.source));
  const cached = await digest(archive);
  if (cached === pins.sourceArchiveSha256) return { action: 'cached', archive };

  await mkdir(cacheDir, { recursive: true });
  const staged = `${archive}.unverified`;
  try {
    await download(pins.source, staged);
    const downloaded = await digest(staged);
    if (downloaded !== pins.sourceArchiveSha256) {
      throw new Error(
        `pinned Node digest mismatch: ${pins.source} produced ${downloaded}, pin expects ${pins.sourceArchiveSha256}`,
      );
    }
    await rename(staged, archive);
  } finally {
    await rm(staged, { force: true });
  }
  return { action: cached === null ? 'downloaded' : 'redownloaded', archive };
}

import { createHash } from 'node:crypto';
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { ensurePinnedNode } from '../scripts/fetch-pinned-node-lib.mjs';

const roots: string[] = [];
afterEach(() => { roots.splice(0).forEach((root) => rmSync(root, { recursive: true, force: true })); });

function cache() {
  const root = mkdtempSync(path.join(os.tmpdir(), 'vc-pinned-node-'));
  roots.push(root);
  return root;
}

const BYTES = 'authentic node archive bytes';
const sha = (text: string) => createHash('sha256').update(text).digest('hex');

const pins = {
  version: 'v22.23.1',
  source: 'https://nodejs.org/dist/v22.23.1/node-v22.23.1-darwin-arm64.tar.gz',
  sourceArchiveSha256: sha(BYTES),
};

/** Records every URL it is asked for and writes whatever `body` says. */
function downloader(body: string) {
  const calls: string[] = [];
  return {
    calls,
    download: (url: string, destination: string) => { calls.push(url); writeFileSync(destination, body); },
  };
}

describe('pinned Node cache', () => {
  it('downloads into an empty cache and verifies the pinned digest', async () => {
    const directory = cache();
    const { download, calls } = downloader(BYTES);

    const result = await ensurePinnedNode({ pins, cacheDir: directory, download });

    expect(result.action).toBe('downloaded');
    expect(calls).toHaveLength(1);
    expect(readFileSync(result.archive, 'utf8')).toBe(BYTES);
  });

  it('takes the URL from the pin rather than a hardcoded constant', async () => {
    const directory = cache();
    const { download, calls } = downloader(BYTES);
    const moved = { ...pins, source: 'https://nodejs.org/dist/v22.23.1/moved-elsewhere.tar.gz' };

    await ensurePinnedNode({ pins: moved, cacheDir: directory, download });

    expect(calls).toEqual([moved.source]);
  });

  it('leaves an authentic cached archive alone', async () => {
    const directory = cache();
    const archive = path.join(directory, path.basename(pins.source));
    writeFileSync(archive, BYTES);
    const { download, calls } = downloader(BYTES);

    const result = await ensurePinnedNode({ pins, cacheDir: directory, download });

    expect(result.action).toBe('cached');
    expect(calls).toEqual([]);
  });

  it('replaces a cached archive whose digest does not match the pin', async () => {
    const directory = cache();
    const archive = path.join(directory, path.basename(pins.source));
    writeFileSync(archive, 'corrupted leftovers');
    const { download, calls } = downloader(BYTES);

    const result = await ensurePinnedNode({ pins, cacheDir: directory, download });

    expect(result.action).toBe('redownloaded');
    expect(calls).toHaveLength(1);
    expect(readFileSync(archive, 'utf8')).toBe(BYTES);
  });

  it('rejects downloaded bytes that do not match the pin and keeps them out of the cache', async () => {
    const directory = cache();
    const archive = path.join(directory, path.basename(pins.source));
    const { download } = downloader('bytes from a compromised mirror');

    await expect(ensurePinnedNode({ pins, cacheDir: directory, download })).rejects.toThrow(/digest/i);

    expect(existsSync(archive)).toBe(false);
  });
});

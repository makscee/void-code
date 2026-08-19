import { randomUUID } from 'node:crypto';
import { lstat, mkdir, open, readFile, rename, rm } from 'node:fs/promises';
import path from 'node:path';
import type { BetaDefensiveState } from './ed25519-beta';
export type { BetaDefensiveState } from './ed25519-beta';

const STATE_KEYS = ['channel', 'keyId', 'manifestDigest', 'schema', 'sequence', 'version'];
function validState(raw: unknown): BetaDefensiveState {
  if (typeof raw !== 'object' || raw === null || Array.isArray(raw)) throw new Error('invalid beta state');
  const value = raw as Record<string, unknown>; const keys = Object.keys(value).sort();
  if (keys.length !== STATE_KEYS.length || keys.some((key, index) => key !== [...STATE_KEYS].sort()[index]) || value.schema !== 1 || value.channel !== 'closed-beta'
    || typeof value.version !== 'string' || !/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)-beta\.([1-9]\d*)$/.test(value.version)
    || !Number.isSafeInteger(value.sequence) || Number(value.sequence) < 1
    || typeof value.manifestDigest !== 'string' || !/^[0-9a-f]{64}$/.test(value.manifestDigest)
    || typeof value.keyId !== 'string' || !/^[a-z0-9][a-z0-9-]{0,63}$/.test(value.keyId)) throw new Error('invalid beta state');
  return value as unknown as BetaDefensiveState;
}

export class BetaStateStore {
  private readonly directory: string; private readonly file: string;
  constructor(userData: string) { this.directory = path.join(userData, 'update-security'); this.file = path.join(this.directory, 'closed-beta-state.json'); }

  async load(): Promise<BetaDefensiveState | undefined> {
    let details;
    try { details = await lstat(this.file); } catch (error) { if ((error as NodeJS.ErrnoException).code === 'ENOENT') return; throw error; }
    if (!details.isFile() || details.isSymbolicLink() || (process.platform !== 'win32' && (details.mode & 0o077) !== 0)) throw new Error('insecure beta state');
    let parsed: unknown; try { parsed = JSON.parse(await readFile(this.file, 'utf8')) as unknown; } catch { throw new Error('corrupt beta state'); }
    return validState(parsed);
  }

  async save(state: BetaDefensiveState): Promise<void> {
    const accepted = validState(state); await mkdir(this.directory, { recursive: true, mode: 0o700 });
    const directory = await lstat(this.directory);
    if (!directory.isDirectory() || directory.isSymbolicLink() || (process.platform !== 'win32' && (directory.mode & 0o077) !== 0)) throw new Error('insecure beta state directory');
    try {
      const existing = await lstat(this.file);
      if (!existing.isFile() || existing.isSymbolicLink() || (process.platform !== 'win32' && (existing.mode & 0o077) !== 0)) throw new Error('insecure beta state target');
    } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error; }
    const temporary = path.join(this.directory, `.closed-beta-state-${randomUUID()}.tmp`);
    const handle = await open(temporary, 'wx', 0o600);
    try { await handle.writeFile(`${JSON.stringify(accepted)}\n`, 'utf8'); await handle.sync(); } finally { await handle.close(); }
    try {
      await rename(temporary, this.file);
      if (process.platform !== 'win32') { const directoryHandle = await open(this.directory, 'r'); try { await directoryHandle.sync(); } finally { await directoryHandle.close(); } }
    } catch (error) { await rm(temporary, { force: true }); throw error; }
  }
}

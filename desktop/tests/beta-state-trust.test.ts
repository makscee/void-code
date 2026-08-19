import { chmod, lstat, mkdir, mkdtemp, readFile, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { BetaStateStore, type BetaDefensiveState } from '../src/main/beta-state';
import { compiledUpdateTrustMode, productionBetaKey } from '../src/main/update-trust';

const state: BetaDefensiveState = { schema: 1, channel: 'closed-beta', version: '0.1.3-beta.2', sequence: 2, manifestDigest: 'a'.repeat(64), keyId: 'beta-key-pending-ceremony' };

describe('defensive beta update state', () => {
  it('atomically persists strict rollback state under userData with private permissions', async () => {
    const root = await mkdtemp(path.join(os.tmpdir(), 'vc-beta-state-'));
    const store = new BetaStateStore(root);
    await store.save(state);
    await expect(store.load()).resolves.toEqual(state);
    expect(JSON.parse(await readFile(path.join(root, 'update-security', 'closed-beta-state.json'), 'utf8'))).toEqual(state);
    if (process.platform !== 'win32') {
      expect((await lstat(path.join(root, 'update-security'))).mode & 0o777).toBe(0o700);
      expect((await lstat(path.join(root, 'update-security', 'closed-beta-state.json'))).mode & 0o777).toBe(0o600);
    }
  });

  it('fails closed for corrupt, unknown-field, permissive, or symlinked state', async () => {
    const { mkdtemp, symlink } = await import('node:fs/promises');
    for (const body of ['{', JSON.stringify({ ...state, extra: true }), JSON.stringify({ ...state, sequence: 0 })]) {
      const root = await mkdtemp(path.join(os.tmpdir(), 'vc-beta-state-')); const dir = path.join(root, 'update-security'); await mkdir(dir); await writeFile(path.join(dir, 'closed-beta-state.json'), body); await chmod(path.join(dir, 'closed-beta-state.json'), 0o600);
      await expect(new BetaStateStore(root).load()).rejects.toThrow();
    }
    if (process.platform !== 'win32') {
      const root = await mkdtemp(path.join(os.tmpdir(), 'vc-beta-state-')); const dir = path.join(root, 'update-security'); await mkdir(dir); const target = path.join(root, 'target'); await writeFile(target, JSON.stringify(state)); await symlink(target, path.join(dir, 'closed-beta-state.json'));
      await expect(new BetaStateStore(root).load()).rejects.toThrow();
    }
  });
});

describe('source-owned trust selection and ceremony gate', () => {
  it('selects closed beta only from the compiled prerelease identity, never remote input', () => {
    expect(compiledUpdateTrustMode('0.1.3-beta.1')).toBe('closed-beta');
    expect(compiledUpdateTrustMode('0.1.3')).toBe('stable');
    expect(compiledUpdateTrustMode('0.1.3-rc.1')).toBe('stable');
  });

  it('ships only the fixed INTERNAL-BETA ceremony public key, never remote input', () => {
    expect(productionBetaKey('internal-beta-2026-08')?.toString('base64url')).toBe('rLWIrvTJV3Sv1pDk-FaYGCNadFEU_7pPD7sBvb_bfAc');
    expect(productionBetaKey('anything-remote')).toBeUndefined();
  });
});

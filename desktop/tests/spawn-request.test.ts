import { mkdirSync } from 'node:fs';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { PrivateRuntime } from '../src/main/resources';
import { spawnDesktopRequest } from '../src/main/spawn-request';

const roots: string[] = [];
afterEach(async () => { vi.unstubAllEnvs(); for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true }); });

describe('desktop session resolution before PTY launch', () => {
  it('does not invoke the injected PTY spawner when resume resolution fails', async () => {
    const home = await mkdtemp(path.join(os.tmpdir(), 'vc-spawn-resolution-')); roots.push(home); vi.stubEnv('HOME', home);
    const cwd = path.join(home, 'work'); mkdirSync(cwd);
    const runtime = { root: home, node: '/private/node', fixture: '/private/fixture', vc: '/private/vc', piEntry: '/private/pi' } as PrivateRuntime;
    const spawn = vi.fn();
    expect(() => spawnDesktopRequest(runtime, { sessionId: '123e4567-e89b-42d3-a456-426614174000', cwd, mode: 'resume' }, spawn)).toThrow('SESSION_MISSING');
    expect(spawn).not.toHaveBeenCalled();
  });
});

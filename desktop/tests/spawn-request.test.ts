import { mkdirSync } from 'node:fs';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { StartRequest } from '../src/shared/contract';
import type { PrivateRuntime } from '../src/main/resources';
import { readFileSync } from 'node:fs';
import { spawnDesktopRequest } from '../src/main/spawn-request';
import { fixtureChildEnv } from '../src/main/desktop-child-env';

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

describe('the fixture session is launched through the shared environment', () => {
  it('hands the fixture exactly what fixtureChildEnv builds for this platform', () => {
    // The behavioural half of the tie. It is honest about what it can prove: this suite runs on
    // macos-14 only, so on every machine that runs it the platform is darwin and the two agree
    // whether or not the call site was changed. It earns its place on a Windows runner and states
    // that limit rather than implying more.
    const runtime = { root: '/private', node: '/private/node', fixture: '/private/fixture', vc: '/private/vc', piEntry: '/private/pi' } as PrivateRuntime;
    const spawn = vi.fn();
    const request = { sessionId: 'fixture-round-trip', fixture: 'roundTrip' } as unknown as StartRequest;
    spawnDesktopRequest(runtime, request, spawn);
    expect(spawn).toHaveBeenCalledOnce();
    expect(spawn.mock.calls[0][2].env).toEqual(fixtureChildEnv(process.platform === 'win32' ? 'win32' : 'darwin', process.env));
  });

  it('builds that environment in one place rather than beside it', () => {
    // The half that works on the machines this actually runs on. An inline object literal here is
    // exactly how the variable came to be missing for four weeks -- the two environments were
    // written next to each other and only one of them was kept up.
    //
    // Honest limit: source text. It says the literal is gone and the shared builder is called, not
    // that a Windows run started.
    const source = readFileSync(new URL('../src/main/spawn-request.ts', import.meta.url), 'utf8');
    expect(source, 'the fixture environment is not built through the shared function').toMatch(/fixtureChildEnv\s*\(/);
    expect(source, 'the fixture environment is still written out here, where it can drift from the session one again').not.toMatch(/VOID_FIXTURE\s*:/);
  });
});

import { mkdirSync } from 'node:fs';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { StartRequest } from '../src/shared/contract';
import type { PrivateRuntime } from '../src/main/resources';
import { readFileSync } from 'node:fs';
import { spawnDesktopRequest } from '../src/main/spawn-request';
import { fixtureChildEnv, type DesktopPlatform } from '../src/main/desktop-child-env';

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

// ---------------------------------------------------------------------------
// The suite runs on macos-14 and nowhere else: `.github/workflows/desktop-tests.yml` is
// `runs-on: macos-14` with a single `npm test`, and the job called `Test (Windows)` is
// `.github/workflows/windows-go-tests.yml`, which runs one `go test` and no Vitest at all.
//
// So the behavioural test above compares the call site against fixtureChildEnv *for this host*,
// and on macOS both sides say darwin. Measured, not assumed: inverting the choice at the call
// site to `? 'darwin' : 'win32'` does make it fail, so that much it holds. What survives it is
// the call site dropping the choice altogether -- `fixtureChildEnv('darwin', process.env)`, the
// literal shape of the four-week defect, the Windows fixture handed an environment with no
// SystemRoot and Node dying on ncrypto::CSPRNG before a line of it runs. With that in place the
// whole file passes, 3 of 3. The win32 branch is executed by nothing here, so nothing here can
// tell a call site that reaches it from one that never does.
//
// fixtureChildEnv takes the platform as an argument for exactly this reason; its own comment
// says so. spawnDesktopRequest does not accept one, it reads process.platform inside, and that
// is the seam that is missing: the argument stops one step short of the caller that chooses it.
// findSessionFiles already solved the same problem the same way, and is the shape to copy --
// `options.platform ?? (process.platform === 'win32' ? 'win32' : 'darwin')`, session-files.ts:120.
// ---------------------------------------------------------------------------
describe('the fixture environment is proved for both platforms from the one machine that runs this', () => {
  const runtime = { root: '/private', node: '/private/node', fixture: '/private/fixture', vc: '/private/vc', piEntry: '/private/pi' } as PrivateRuntime;
  const request = { sessionId: 'fixture-round-trip', fixture: 'roundTrip' } as unknown as StartRequest;

  // Passing the host platform in is the whole point: with it, the branch that only ever runs on
  // Windows is executed here, on macOS, by the suite that actually runs. Omitting it must keep the
  // shipped behaviour -- index.ts calls this with four arguments and must go on doing so.
  function fixtureEnv(hostPlatform?: DesktopPlatform): Record<string, string> {
    const spawn = vi.fn();
    spawnDesktopRequest(runtime, request, spawn, undefined, hostPlatform);
    expect(spawn).toHaveBeenCalledOnce();
    return spawn.mock.calls[0][2].env as Record<string, string>;
  }

  const windows = { SystemRoot: 'D:\\Windows', PATH: 'D:\\Windows\\System32', TERM: 'xterm-256color', COLORTERM: 'truecolor', VOID_FIXTURE: 'owned' };
  const darwin = { PATH: '/usr/bin:/bin', TERM: 'xterm-256color', COLORTERM: 'truecolor', VOID_FIXTURE: 'owned' };

  it('launches the Windows fixture with SystemRoot, checked from macOS', () => {
    // Written out rather than compared against a fixtureChildEnv('win32', ...) call, because a
    // comparison against the builder is satisfied by any call site that reaches the same builder,
    // including one handed the wrong platform. This says what Node needs to start, in full.
    // SystemRoot is stubbed to a value no machine has, so a constant would not pass for a lookup.
    vi.stubEnv('SystemRoot', 'D:\\Windows');
    expect(fixtureEnv('win32')).toEqual(windows);
  });

  it('launches the Darwin fixture with the bare PATH and no system root', () => {
    vi.stubEnv('SystemRoot', 'D:\\Windows');
    expect(fixtureEnv('darwin')).toEqual(darwin);
  });

  it('keeps the host as the default, so the shipped call site is still the one under test', () => {
    // The two above prove both branches of fixtureChildEnv are wired; this proves the caller maps
    // the host to the matching one rather than to the other. The expectation is one of the two
    // literals above -- not another call to the builder -- so inverting the choice at the call site
    // has somewhere to fail.
    vi.stubEnv('SystemRoot', 'D:\\Windows');
    expect(fixtureEnv()).toEqual(process.platform === 'win32' ? windows : darwin);
  });
});

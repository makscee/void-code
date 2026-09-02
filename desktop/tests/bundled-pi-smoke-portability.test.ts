import { readFileSync } from 'node:fs';
import { stripComments } from '../scripts/pi-bun-contract-lib.mjs';
import { describe, expect, it, vi } from 'vitest';

// win32 has no executable verification path at all, and the panel's advisor was right that this is
// one hole rather than three. The bundled smoke silences itself on Windows, bundles Pi for whatever
// platform it happens to be running on, and both jobs that call it run on macOS -- so what gets
// checked is a darwin bundle that production does not ship, while the Windows job packages an
// installer nobody runs.
//
// Fixing the refusal alone would move the failure one step along: the smoke also hands the child
// PATH=/usr/bin:/bin and HOME, neither of which means anything on Windows. So the seam is the whole
// launch environment, not the stub.
//
// What must not happen while closing it, and it is written into the plan as a prohibition: nothing
// here may be made weaker on the target platform. A check that is thinner on Windows than elsewhere
// is worse than today's honest refusal, because it reports success. Every assertion below is
// therefore stated for both platforms, and the ones that differ differ only where the operating
// system genuinely differs.
//
// Honest limit: these pin the shape of the launch -- what is bundled, what environment the child is
// given, where the stub comes from. They do not run a bundle on Windows. That is the workflow step
// pinned in windows-app-artifact-workflow.test.ts, and only a real runner can settle it.

type Target = 'win32-x64' | 'darwin-arm64' | 'darwin-x64';
type Smoke = {
  piSmokeTarget: (input: { argv: string[]; env: Record<string, string | undefined> }) => Target;
  piSmokeRunEnv: (input: { target: Target; home: string; packageDir: string }) => Record<string, string>;
  piSmokeBootstrapPlan: (input: { target: Target; directory: string }) => { source: string; output: string };
  piSmokeExpectedNative: (input: { target: Target }) => string[];
  bundleForSmoke: (input: {
    piRoot: string;
    target: Target;
    bundle: (piRoot: string, platform: string) => Promise<unknown>;
    list: (bundleRoot: string) => Promise<string[]>;
  }) => Promise<unknown>;
  inSmokeWorkspace: <T>(
    handlers: { create: () => Promise<string>; remove: (workspace: string) => Promise<void> },
    body: (workspace: string) => Promise<T>,
  ) => Promise<T>;
};
// Imported per test so a missing module reds each one under its own name rather than collapsing the
// file into "no tests", which reads as though the suite shrank.
const smoke = async (): Promise<Smoke> => await import('../scripts/bundled-pi-smoke-lib.mjs') as unknown as Smoke;

describe('the bundled smoke checks the platform it was asked about', () => {
  it('takes its target from the task rather than from wherever it happens to be running', async () => {
    const { piSmokeTarget } = await smoke();
    // The substitution that makes a green check meaningless: `${process.platform}-${process.arch}`
    // bundles darwin on a macOS runner and reports success for a configuration production does not
    // ship. The host is not an input here at all, so it cannot be consulted by accident.
    expect(piSmokeTarget({ argv: ['--target', 'win32-x64'], env: {} })).toBe('win32-x64');
    expect(piSmokeTarget({ argv: [], env: { VC_BUNDLE_SMOKE_TARGET: 'darwin-arm64' } })).toBe('darwin-arm64');
  });

  it('refuses to guess a target rather than falling back to the host', async () => {
    const { piSmokeTarget } = await smoke();
    // A default would keep the defect available: the Windows job would go green while checking
    // whatever the runner happened to be. Saying so out loud is what the refusal is for.
    expect(() => piSmokeTarget({ argv: [], env: {} })).toThrow();
    expect(() => piSmokeTarget({ argv: ['--target', 'linux-riscv64'], env: {} })).toThrow();
  });
});

describe('the launch environment is built for the target, and built from nothing', () => {
  const posix = { target: 'darwin-arm64' as const, home: '/tmp/home', packageDir: '/tmp/pkg' };
  const windows = { target: 'win32-x64' as const, home: 'C:\\Users\\runneradmin', packageDir: 'C:\\pkg' };

  it('points Pi at the settings directory through the variable each system actually reads', async () => {
    const { piSmokeRunEnv } = await smoke();
    // The second of the three reasons win32 fails today. Pi resolves its agent directory from HOME
    // on POSIX and USERPROFILE on Windows; handing Windows a HOME means Pi looks somewhere nobody
    // wrote to, and the smoke reports a missing provider for a reason that has nothing to do with
    // the bundle.
    expect(piSmokeRunEnv(posix).HOME).toBe(posix.home);
    expect(piSmokeRunEnv(windows).USERPROFILE).toBe(windows.home);
    expect(piSmokeRunEnv(windows).HOME, 'a Windows run was handed a POSIX home as well, so which one wins is left to Pi').toBeUndefined();
  });

  it('gives the child a PATH its own system can use', async () => {
    const { piSmokeRunEnv } = await smoke();
    // The third reason. /usr/bin:/bin on Windows is not a narrower PATH, it is an empty one: the
    // child cannot start the processes it needs and the failure looks like a broken bundle.
    expect(piSmokeRunEnv(posix).PATH).toContain('/usr/bin');
    const windowsPath = piSmokeRunEnv(windows).PATH;
    expect(windowsPath, 'the Windows run was handed POSIX system directories').not.toContain('/usr/bin');
    expect(windowsPath.toLowerCase(), 'the Windows run was given no system directory to find its own tools in').toContain('system32');
  });

  it('keeps the same non-negotiables on every platform, because a thinner check is the worst outcome', async () => {
    const { piSmokeRunEnv } = await smoke();
    // The plan forbids a Windows-only weakening in as many words. Stated as a comparison rather
    // than as two separate lists, so that dropping one of these on Windows alone is what fails.
    for (const input of [posix, windows]) {
      const env = piSmokeRunEnv(input);
      expect(env.PI_PACKAGE_DIR, `${input.target} was not told where Pi's package directory is`).toBe(input.packageDir);
      expect(env.TERM, `${input.target} was not given a dumb terminal`).toBe('dumb');
      expect(env.PATH, `${input.target} was given no PATH`).toBeTruthy();
    }
  });

  it('is assembled from nothing, so the machine it runs on cannot make it pass', async () => {
    const { piSmokeRunEnv } = await smoke();
    // The property that keeps this honest: with an inherited environment, a developer's own Pi
    // settings or an ambient VC_BOOTSTRAP_EXECUTABLE could satisfy the run without the bundle
    // having anything to do with it.
    process.env.VC_BUNDLED_SMOKE_CANARY = 'leaked';
    try {
      for (const input of [posix, windows]) {
        expect(piSmokeRunEnv(input).VC_BUNDLED_SMOKE_CANARY, `${input.target} inherited the ambient environment`).toBeUndefined();
      }
    } finally { delete process.env.VC_BUNDLED_SMOKE_CANARY; }
  });
});

describe('one bootstrap stub, not one per platform', () => {
  it('builds every platform\'s stub from a single source', async () => {
    const { piSmokeBootstrapPlan } = await smoke();
    // The condition the plan attaches to the Go stub, and the reason for it is one we have already
    // paid for: the relay's public path list lives in two places and drifted, twice. A shell script
    // kept for POSIX beside a Go binary for Windows is the same arrangement -- two fixtures for one
    // role -- and they will disagree about what a bootstrap answers long before anybody notices.
    //
    // The output may differ, because an executable is named differently on Windows. The source may
    // not: that is what "one stub" means and it is the only half worth checking.
    const built = (['darwin-arm64', 'win32-x64'] as const).map((target) => piSmokeBootstrapPlan({ target, directory: '/work' }));
    expect(new Set(built.map((plan) => plan.source)).size, 'the platforms are built from different bootstrap sources, so they can disagree').toBe(1);
    expect(built.every((plan) => plan.output !== ''), 'a platform got no bootstrap to run').toBe(true);
  });
});

// ---------------------------------------------------------------------------
// The gap the whole previous round left open. piSmokeTarget is pinned four ways over, but between
// "the function returned the right target" and "that target is what the bundle was built with"
// there was one unpinned line, and swapping it back to `${process.platform}-${process.arch}` left
// 854 tests green.
//
// No runner will catch it either: on all three jobs the host equals the target, and there the
// mutation means nothing. It only shows where the two differ -- which, since assemblyTarget refuses
// a cross-operating-system pair, means same OS and different architecture: asking a darwin-arm64
// machine for darwin-x64. That is a real configuration and the macOS runner can do it today, but
// nothing runs it, so the pin has to be here.
//
// It is closed by behaviour rather than by reading the script: bundlePiRuntime copies the native
// module of the platform it was given, and those paths differ per target, so the bundle can be
// asked what it was built for. The bundler is injected here, which is what lets the suite see the
// platform that reached it without an esbuild run or a provisioned Pi tree.
// ---------------------------------------------------------------------------
describe('the bundle is built for the target that was asked for', () => {
  const targets = ['darwin-arm64', 'darwin-x64', 'win32-x64'] as const;

  it('expects a different native module for every target, which is what makes a wrong one visible', async () => {
    const { piSmokeExpectedNative } = await smoke();
    // If two targets expected the same file, a bundle built for the host would satisfy a check
    // written for the target and this whole approach would prove nothing.
    const expected = targets.map((target) => piSmokeExpectedNative({ target }).join('|'));
    expect(new Set(expected).size, `two targets expect the same native module: ${expected.join(' / ')}`).toBe(targets.length);
    expect(expected.every((paths) => paths.endsWith('.node')), 'a target expects no native module at all').toBe(true);
  });

  it('bundles with the target it was given, never with the host', async () => {
    const { bundleForSmoke } = await smoke();
    // The mutation itself, in the one place the suite can see it. The recorded argument is compared
    // against the requested target, so `${process.platform}-${process.arch}` fails here on any
    // machine -- including a runner where the two happen to agree, which is where every real run of
    // this check takes place.
    const asked: string[] = [];
    const target = targets.find((candidate) => candidate !== `${process.platform}-${process.arch}`) as Target;
    await bundleForSmoke({
      piRoot: '/work/pi',
      target,
      bundle: async (_piRoot, platform) => { asked.push(platform); return { entry: '/work/pi/agent/pi~BUN.mjs' }; },
      list: async () => (await smoke()).piSmokeExpectedNative({ target }),
    });
    expect(asked, `the bundler was asked for ${asked.join(', ')} when the task said ${target}`).toEqual([target]);
  });

  it('refuses a bundle carrying another target\'s native module', async () => {
    const { bundleForSmoke, piSmokeExpectedNative } = await smoke();
    // What the mutation produces, seen from the other side: the bundle is real and complete, it is
    // simply for the wrong machine. On the target it would fail to start; here it is caught before
    // anything is reported as verified.
    const host = 'darwin-arm64' as const;
    const target = 'darwin-x64' as const;
    await expect(bundleForSmoke({
      piRoot: '/work/pi',
      target,
      bundle: async () => ({ entry: '/work/pi/agent/pi~BUN.mjs' }),
      list: async () => piSmokeExpectedNative({ target: host }),
    })).rejects.toThrow();
  });

  it('accepts a bundle carrying its own target\'s native module, and hands the result back', async () => {
    const { bundleForSmoke, piSmokeExpectedNative } = await smoke();
    const built = { entry: '/work/pi/agent/pi~BUN.mjs' };
    await expect(bundleForSmoke({
      piRoot: '/work/pi',
      target: 'win32-x64',
      bundle: async () => built,
      list: async () => ['agent/pi~BUN.mjs', ...piSmokeExpectedNative({ target: 'win32-x64' })],
    })).resolves.toBe(built);
  });
});

// ---------------------------------------------------------------------------
// The workspace has to be removed on the way out of a failure, and the reason it was not is worth
// keeping: `die()` ended the run with process.exit, and process.exit does not run `finally`. Proved
// by running it, not by reading the docs -- `node -e 'try { process.exit(1) } finally {
// console.log("ran") }'` prints nothing. Twelve failure sites sat after mkdtemp, so every red run
// left an unpacked Pi tree, a bundle and a stub behind: 215 MB measured in one run, 512 MB over a
// session.
//
// It leaked exactly when the check found what it exists to find, and the person who meets that path
// is mid-bump of the Pi pin, iterating on red, on their own machine rather than an ephemeral runner.
//
// The root was not the twelve calls but that the file had TWO ways to fail -- `die()` exiting and
// ordinary `throw` from bundleForSmoke -- which behaved differently. That is why the first attempt
// to reproduce the leak failed: it forced the throwing kind, and `finally` ran.
// ---------------------------------------------------------------------------
describe('the workspace is removed however the smoke ends', () => {
  const workspace = '/tmp/bundled-pi-smoke-fixture';
  const handlers = () => {
    const order: string[] = [];
    return {
      order,
      create: vi.fn(async () => { order.push('create'); return workspace; }),
      remove: vi.fn(async () => { order.push('remove'); }),
    };
  };

  it('creates, runs, removes, and hands back what the body produced', async () => {
    const { inSmokeWorkspace } = await smoke();
    const { order, create, remove } = handlers();
    await expect(inSmokeWorkspace({ create, remove }, async (given) => { order.push(`body ${given}`); return 'checked'; })).resolves.toBe('checked');
    expect(order).toEqual(['create', `body ${workspace}`, 'remove']);
  });

  it('removes the workspace when the body fails, which is the case that leaked', async () => {
    const { inSmokeWorkspace } = await smoke();
    const { create, remove } = handlers();
    const failure = new Error('BUNDLED PI SMOKE: RED');
    // The original error, not a wrapper: the smoke's message is the whole diagnosis, and a cleanup
    // layer that rewrites it turns a readable refusal into "something went wrong during cleanup".
    await expect(inSmokeWorkspace({ create, remove }, async () => { throw failure; })).rejects.toBe(failure);
    expect(remove, 'a failing run left its workspace on disk').toHaveBeenCalledOnce();
  });

  it('removes the workspace it was given, not one it worked out for itself', async () => {
    const { inSmokeWorkspace } = await smoke();
    const { create, remove } = handlers();
    await inSmokeWorkspace({ create, remove }, async () => undefined);
    expect(remove).toHaveBeenCalledWith(workspace);
  });

  it('has nothing to remove when the workspace was never created', async () => {
    const { inSmokeWorkspace } = await smoke();
    const { remove } = handlers();
    const body = vi.fn(async () => undefined);
    const failure = new Error('mkdtemp failed');
    await expect(inSmokeWorkspace({ create: async () => { throw failure; }, remove }, body)).rejects.toBe(failure);
    expect(body, 'the body ran without a workspace to run in').not.toHaveBeenCalled();
    expect(remove, 'a workspace that was never created was removed anyway').not.toHaveBeenCalled();
  });

  it('lets the smoke\'s own failure through when removing the workspace fails too', async () => {
    const { inSmokeWorkspace } = await smoke();
    const { create } = handlers();
    const failure = new Error('BUNDLED PI SMOKE: RED');
    // Both went wrong; only one of them tells the reader what the check found. A cleanup error that
    // replaced it would leave the person mid-bump with a message about a directory.
    await expect(inSmokeWorkspace({ create, remove: async () => { throw new Error('EBUSY'); } }, async () => { throw failure; })).rejects.toBe(failure);
  });
});

describe('there is one way out of the smoke, and it runs the cleanup', () => {
  it('reaches no process.exit before the workspace has been unwound', () => {
    // The cost of the chosen shape, named by the implementer himself: now that `finally` is the only
    // cleanup, the only safe way to end badly is to throw. A thirteenth failure added later with
    // process.exit would leak again, and nothing else in this suite would notice -- the same two
    // ways of failing that already diverged in this very file once.
    //
    // Honest limits, and they are real: this is source text, it knows only the name `main`, and it
    // would miss process.abort(), a process.exit inside a module this one imports, or a rename of
    // main. It catches the regression that actually happened, and says nothing about the rest.
    // Comments are removed first, and not out of caution: written the obvious way this very check
    // went red on the comment that explains why process.exit is wrong, which quotes it. Same trap we
    // fixed in the Pi contract two rounds ago, so the same stripper is reused rather than written a
    // second time -- one implementation, not two that can disagree.
    const source = stripComments(readFileSync(new URL('../scripts/check-bundled-pi-smoke.mjs', import.meta.url), 'utf8'));
    const handler = source.indexOf('await main()');
    expect(handler, 'could not find the `await main()` this check is anchored to').toBeGreaterThan(-1);
    const above = source.slice(0, handler);
    expect((source.match(/process\s*\.\s*exit/g) ?? []).length, 'the smoke ends the process in more than one place, so which one runs the cleanup is a question again').toBe(1);
    expect(above, 'something above `await main()` ends the process with process.exit, and process.exit does not run finally -- the workspace it created stays on disk').not.toMatch(/process\s*\.\s*exit/);
  });
});

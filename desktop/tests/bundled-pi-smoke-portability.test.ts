import { describe, expect, it } from 'vitest';

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

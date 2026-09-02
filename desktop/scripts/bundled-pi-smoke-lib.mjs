// How the bundled Pi smoke is launched, decided without touching a disk or a process.
//
// It exists because win32 had no executable verification path at all, and that was one hole rather
// than three: the smoke refused on Windows, bundled Pi for whatever platform it happened to be
// running on, and both jobs calling it ran on macOS -- so what was checked was a darwin bundle that
// production does not ship, while the Windows job uploaded an installer nobody had started.
//
// Everything here is a decision, so it can be pinned by fixtures rather than by a runner. What a
// runner has to settle -- does a win32 bundle actually load its extension -- is settled by the step
// in desktop-windows-app.yml, and by nothing in this file.
import path from 'node:path';
import { ASSEMBLY_PLATFORM_NAMES } from './resource-assembly-lib.mjs';

// Windows needs more than a PATH to behave: SystemRoot is what winsock and os.tmpdir() fall back
// to, and PATHEXT is how a spawned name becomes an executable. Constants rather than values read
// from the machine, because the whole point of this environment is that the machine cannot
// contribute to it. A runner with a relocated Windows directory would fail loudly here, which is
// the right direction to be wrong in.
const WINDOWS_ROOT = 'C:\\Windows';
const WINDOWS_PATH = [`${WINDOWS_ROOT}\\System32`, WINDOWS_ROOT, `${WINDOWS_ROOT}\\System32\\Wbem`].join(';');
const POSIX_PATH = '/usr/bin:/bin';

/**
 * Which platform the smoke was asked to check.
 *
 * The host is not an input, deliberately: `${process.platform}-${process.arch}` is what made a green
 * run meaningless, and a parameter that does not exist cannot be consulted by accident. There is no
 * default either -- a default would let the Windows job go green while checking whatever the runner
 * happened to be, which is the defect this whole seam exists to remove.
 */
export function piSmokeTarget({ argv, env }) {
  const flag = argv.findIndex((argument) => argument === '--target');
  const inline = argv.find((argument) => argument.startsWith('--target='));
  const asked = flag >= 0 ? argv[flag + 1] : inline ? inline.slice('--target='.length) : env.VC_BUNDLE_SMOKE_TARGET;
  if (!asked) {
    throw new Error(`the bundled Pi smoke was not told which platform to check.\nPass --target <${ASSEMBLY_PLATFORM_NAMES.join('|')}> or set VC_BUNDLE_SMOKE_TARGET.\nThere is no default on purpose: the host is what a green run must never quietly mean.`);
  }
  if (!ASSEMBLY_PLATFORM_NAMES.includes(asked)) {
    throw new Error(`the bundled Pi smoke has no target called ${asked}; it checks ${ASSEMBLY_PLATFORM_NAMES.join(', ')}`);
  }
  return asked;
}

/**
 * The environment the bundled Pi is launched with, built from nothing.
 *
 * From nothing is the property that keeps the check honest: with an inherited environment a
 * developer's own Pi settings, or an ambient VC_BOOTSTRAP_EXECUTABLE, could satisfy the run without
 * the bundle having had anything to do with it.
 *
 * The two platforms differ only where the operating systems do. Pi reads its agent directory from
 * HOME on POSIX and USERPROFILE on Windows, so handing Windows a HOME points Pi at a directory
 * nobody wrote to and the smoke reports a missing provider for a reason that has nothing to do with
 * the bundle. /usr/bin:/bin on Windows is not a narrower PATH but an empty one. Everything else is
 * identical by construction: a check that is thinner on the target platform than elsewhere is worse
 * than an honest refusal, because it reports success.
 */
export function piSmokeRunEnv({ target, home, packageDir }) {
  const common = { TERM: 'dumb', PI_PACKAGE_DIR: packageDir };
  if (target.startsWith('win32')) {
    return { ...common, PATH: WINDOWS_PATH, SystemRoot: WINDOWS_ROOT, PATHEXT: '.COM;.EXE;.BAT;.CMD', USERPROFILE: home };
  }
  return { ...common, PATH: POSIX_PATH, HOME: home };
}

/**
 * Where the bootstrap stub is built from, and to.
 *
 * One source for every platform, and that is the half worth checking. The extension asks vc for its
 * providers through execFileSync without a shell, and Node has refused to spawn .cmd and .bat that
 * way since 18.20 -- so the shell script the smoke used to write could never run on Windows. A Go
 * program answers on all three systems, and Go is already a first-class toolchain here: the Windows
 * assembly cross-compiles vc for windows/amd64 a few steps earlier.
 *
 * Keeping the shell script for POSIX and adding a binary for Windows was the tempting shape and is
 * the one we have already paid for: the relay's public path list lives in two places and drifted,
 * twice. Two fixtures for one role disagree about what a bootstrap answers long before anybody
 * notices. So the output may differ -- an executable is named differently on Windows -- and the
 * source may not.
 *
 * Rejected, so the next reader does not re-derive them: node.exe as the executable (the extension
 * fixes the arguments to ['pi-bootstrap'], so there is nowhere to put a script path); the real
 * vc.exe (it would make the smoke depend on a relay and a live bootstrap, when what it checks is
 * extension loading); weakening the check on Windows (the plan forbids it in as many words);
 * committing a built .exe (an opaque binary in the tree is worse than a build dependency).
 */
export function piSmokeBootstrapPlan({ target, directory }) {
  return {
    source: path.resolve(import.meta.dirname, 'pi-smoke-bootstrap'),
    output: path.join(directory, target.startsWith('win32') ? 'pi-smoke-bootstrap.exe' : 'pi-smoke-bootstrap'),
  };
}

import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import * as assembly from '../scripts/resource-assembly-lib.mjs';
import { ensurePinnedNode } from '../scripts/fetch-pinned-node-lib.mjs';
import * as runtime from '../src/main/resources';
import pins from '../scripts/resource-pins.json';

// The desktop app is built for Apple silicon and for nothing else. `package:mac`
// has said `electron-builder --dir --mac --arm64` since the day the scaffold
// landed, and every pin under it was written for one architecture. Where, read
// off the files: resource-assembly-lib.mjs builds the archive name from a
// template literal with `darwin-arm64` in it; assemble-resources.mjs refuses to
// start unless `process.arch` is arm64, passes GOARCH=arm64 to `go build`, and
// stamps `platform: 'darwin-arm64'` on the manifest; src/main/resources.ts
// accepts no other manifest on anything that is not Windows; package:mac ends in
// `--arm64`. None of that was a decision against Intel. It is one architecture,
// written down in each place that needed one, because there was only ever one.
//
// This file states what the pins have to become for there to be two, and it
// picks the shape deliberately: the platform is a KEY, not a spelling baked into
// a code path. `nodePinFor(pins, 'darwin-x64')` is how every consumer -- the
// cache fetcher, the assembly, the workflows, these tests -- asks for a pin, so
// a third architecture is a new entry in one map rather than a new branch in
// each consumer. The pin that comes back carries its own platform, which is what
// lets the checks below decide, from the pin alone, whether the binary it
// describes can be RUN here or only hashed.
//
// That last point is the trap this file exists for. The Intel bundle is built on
// the same Apple silicon runner as the other one -- see
// mac-intel-app-artifact-workflow.test.ts -- and a darwin-x64 binary produced
// there cannot be executed there. Rosetta is not something a runner image
// promises, and nothing here checks whether GitHub still offers an Intel image;
// the design simply does not ask for one. So every place the assembly today
// learns something by RUNNING a binary it just produced is a place a cross build
// dies. There are three of them, and each is pinned below as a rule about what
// may be executed, so the answer is written down once rather than rediscovered
// on a red runner.
//
// What this file does not do: it does not run a cross build. It cannot -- that
// needs a Go toolchain, the pinned archives and a packager, and it produces a
// bundle nothing here could launch anyway. It fixes the DECISIONS a cross build
// takes. Whether the bundle those decisions produce starts on a real Intel Mac
// is unverified, deliberately and visibly.

const temporary: string[] = [];
function temp() {
  const directory = mkdtempSync(path.join(os.tmpdir(), 'vc-mac-intel-'));
  temporary.push(directory);
  return directory;
}
afterEach(() => { temporary.splice(0).forEach((directory) => rmSync(directory, { recursive: true, force: true })); });

const sha = (bytes: string) => createHash('sha256').update(bytes).digest('hex');

type NodePin = {
  platform: string;
  version: string;
  source: string;
  sourceArchiveSha256: string;
  executableSha256?: string;
};

// The new exports are reached through the namespace rather than by name. A named
// import of something the module does not export yet is a load-time failure that
// takes the whole file down and reports no test count at all; reached this way,
// each rule below fails on its own and says which one.
const nodePinFor = (file: unknown, platform: string): NodePin =>
  (assembly as unknown as { nodePinFor(file: unknown, platform: string): NodePin }).nodePinFor(file, platform);
const assemblyTarget = (target: string, host: string) =>
  (assembly as unknown as { assemblyTarget(target: string, host: string): { platform: string; goos: string; goarch: string; native: boolean } })
    .assemblyTarget(target, host);
const vcBuildPlan = (target: { platform: string; goos: string; goarch: string; native: boolean }, host: string) =>
  (assembly as unknown as { vcBuildPlan(target: unknown, host: string): ReadonlyArray<{ goos: string; goarch: string; purpose: string }> })
    .vcBuildPlan(target, host);
const stagedNpmVersion = (stagingRoot: string) =>
  (assembly as unknown as { stagedNpmVersion(stagingRoot: string): string }).stagedNpmVersion(stagingRoot);
const expectedRuntimePlatform = (platform: string, arch: string) =>
  (runtime as unknown as { expectedRuntimePlatform(platform: string, arch: string): string }).expectedRuntimePlatform(platform, arch);

// ---------------------------------------------------------------------------
// The pins, asked for by platform.
//
// Today resource-pins.json holds the arm64 Node pin at the top level under
// `node` and the Windows one under `windows.node` -- two shapes for the same
// kind of fact, and no room for a third that is neither. The lookup below is the
// whole design decision: one map, keyed by the platform identifier the rest of
// this codebase already uses (`${process.platform}-${process.arch}`, which is
// what the runtime manifest says and what src/main/resources.ts compares
// against). Adding an architecture is adding a key.
// ---------------------------------------------------------------------------

describe('a Node pin is asked for by platform', () => {
  it('is read out of the file rather than switched on in code', () => {
    // The lookup has to be data-driven, or "a new architecture is a new entry"
    // is not true: a switch over three known names would answer this synthetic
    // file with a throw. The pin comes back carrying its platform, because every
    // check downstream needs to know which architecture it is holding.
    const synthetic = {
      platforms: {
        'linux-x64': {
          node: {
            version: 'v22.23.1',
            source: 'https://nodejs.org/dist/v22.23.1/node-v22.23.1-linux-x64.tar.gz',
            sourceArchiveSha256: '1'.repeat(64),
          },
        },
      },
    };
    expect(nodePinFor(synthetic, 'linux-x64')).toEqual({
      platform: 'linux-x64',
      version: 'v22.23.1',
      source: 'https://nodejs.org/dist/v22.23.1/node-v22.23.1-linux-x64.tar.gz',
      sourceArchiveSha256: '1'.repeat(64),
    });
  });

  it('names what it does have when asked for a platform it does not pin', () => {
    // The message is the thing being pinned. "undefined is not an object" three
    // frames down tells a person nothing; the list of platforms tells them
    // exactly which line to add.
    let thrown = 'nothing was thrown for an unpinned platform';
    try { nodePinFor(pins, 'linux-arm64'); } catch (error) { thrown = (error as Error).message; }
    expect(thrown).toContain('linux-arm64');
    expect(thrown).toContain('darwin-x64');
  });

  it('still holds the Apple silicon pin, byte for byte, at the values it has today', () => {
    // The Intel build must not disturb the one that works. These four values are
    // what today's arm64 bundle is assembled from; moving them into the map is a
    // move, not an edit.
    expect(nodePinFor(pins, 'darwin-arm64')).toEqual({
      platform: 'darwin-arm64',
      version: 'v22.23.1',
      source: 'https://nodejs.org/dist/v22.23.1/node-v22.23.1-darwin-arm64.tar.gz',
      sourceArchiveSha256: 'ef28d8fab2c0e4314522d4bb1b7173270aa3937e93b92cb7de79c112ac1fa953',
      executableSha256: '2e3f1286a7eb3736346ed1803e458a0ff909e2b2d5bc746144dcb76970e9b99d',
    });
  });

  it('holds the Intel Node pin at the digests nodejs.org publishes for it', () => {
    // The archive digest is the one in the official SHASUMS256.txt for v22.23.1
    // (b8da98...), confirmed by downloading the archive and hashing it here. The
    // executable digest is the sha256 of node-v22.23.1-darwin-x64/bin/node
    // inside that archive, taken the same way -- it is what assertNodePin
    // compares the staged binary against, and there is no other way to obtain it
    // than to unpack the authenticated archive once.
    expect(nodePinFor(pins, 'darwin-x64')).toEqual({
      platform: 'darwin-x64',
      version: 'v22.23.1',
      source: 'https://nodejs.org/dist/v22.23.1/node-v22.23.1-darwin-x64.tar.gz',
      sourceArchiveSha256: 'b8da981b8a0b1241b70249204916da76c63573ddf5814dbd2d1e41069105cb81',
      executableSha256: '03afb3618a2685335209c93f8c34633f8316dbe6cc32196bc19daa1a73852e5b',
    });
  });

  it('answers for Windows through the same map, so there is one way to ask', () => {
    // The Windows Node pin is the same kind of fact and has to be reachable the
    // same way, or the map is not a map -- it is the arm64 pin with a friend.
    // What the Windows entry carries beyond Node (the pinned vc.exe) is not this
    // file's business and is left where it is.
    expect(nodePinFor(pins, 'win32-x64')).toMatchObject({
      platform: 'win32-x64',
      version: 'v22.23.1',
      source: 'https://nodejs.org/dist/v22.23.1/node-v22.23.1-win-x64.zip',
      sourceArchiveSha256: '7df0bc9375723f4a86b3aa1b7cc73342423d9677a8df4538aca31a049e309c29',
    });
  });

  it('describes every platform it pins in the same fields', () => {
    // Uniformity is what makes tomorrow's entry a copy of today's. An entry that
    // needed one extra key would need one extra branch in every reader.
    const shapes = ['darwin-arm64', 'darwin-x64'].map((platform) => Object.keys(nodePinFor(pins, platform)).sort().join(', '));
    expect(shapes[1]).toBe(shapes[0]);
  });
});

// ---------------------------------------------------------------------------
// The archive name, derived from the platform rather than written into it.
//
// expectedNodeArchive builds `node-${version}-darwin-arm64.tar.gz` from a
// template literal with the architecture inside it, and then checks the pin's
// source URL against it. That check is the reason the function exists -- a pin
// pointing somewhere other than the official archive for exactly this version
// and platform is refused before a byte is read. It has to keep refusing, per
// platform, once there is more than one.
// ---------------------------------------------------------------------------

describe('the pinned Node archive is named after the platform the pin is for', () => {
  it('names the Apple silicon archive exactly as it does today', () => {
    expect(assembly.expectedNodeArchive(nodePinFor(pins, 'darwin-arm64'))).toEqual({
      archiveName: 'node-v22.23.1-darwin-arm64.tar.gz',
      root: 'node-v22.23.1-darwin-arm64',
      source: 'https://nodejs.org/dist/v22.23.1/node-v22.23.1-darwin-arm64.tar.gz',
    });
  });

  it('names the Intel archive from the same pin, not a second code path', () => {
    expect(assembly.expectedNodeArchive(nodePinFor(pins, 'darwin-x64'))).toEqual({
      archiveName: 'node-v22.23.1-darwin-x64.tar.gz',
      root: 'node-v22.23.1-darwin-x64',
      source: 'https://nodejs.org/dist/v22.23.1/node-v22.23.1-darwin-x64.tar.gz',
    });
  });

  it('refuses a pin whose URL is for a different architecture than its platform', () => {
    // The failure this catches is a copy-paste: a new entry added to the map
    // with the old entry's URL still in it. Without the platform in the
    // comparison, the arm64 archive would be fetched, hashed against the arm64
    // digest, staged, and shipped inside a bundle labelled Intel.
    const crossed = { ...nodePinFor(pins, 'darwin-x64'), source: 'https://nodejs.org/dist/v22.23.1/node-v22.23.1-darwin-arm64.tar.gz' };
    expect(() => assembly.expectedNodeArchive(crossed)).toThrow('source identifier mismatch');
  });

  it('still refuses an attacker source and an unreadable version', () => {
    const attacked = { ...nodePinFor(pins, 'darwin-x64'), source: 'https://attacker.invalid/node.tar.gz' };
    expect(() => assembly.expectedNodeArchive(attacked)).toThrow('source identifier mismatch');
    const unversioned = { ...nodePinFor(pins, 'darwin-x64'), version: 'latest' };
    expect(() => assembly.expectedNodeArchive(unversioned)).toThrow('invalid private Node version pin');
  });
});

// ---------------------------------------------------------------------------
// The cache fetcher follows the pin it is handed.
//
// `npm run setup` downloads the archive named by the pin. With two pins it has
// to be able to download either one, and the transport must not learn the
// architecture from anywhere but the pin -- the destination filename included,
// because two archives sharing one filename is a cache that silently serves the
// wrong Node.
// ---------------------------------------------------------------------------

describe('the pinned Node cache holds one archive per platform', () => {
  it('downloads the Intel archive under its own name, beside the arm64 one', async () => {
    // One cache directory, two archives. They differ only by the architecture
    // in the filename, which comes from the pin's source URL -- so a fetcher
    // that took the name from anywhere else would have the two overwrite each
    // other and serve whichever ran last.
    const cacheDir = temp();
    const bytes = 'authentic intel node archive bytes';
    const calls: string[] = [];
    const pin = { ...nodePinFor(pins, 'darwin-x64'), sourceArchiveSha256: sha(bytes) };

    const result = await ensurePinnedNode({
      pins: pin,
      cacheDir,
      download: (url: string, destination: string) => { calls.push(url); writeFileSync(destination, bytes); },
    });

    expect(calls).toEqual(['https://nodejs.org/dist/v22.23.1/node-v22.23.1-darwin-x64.tar.gz']);
    expect(path.basename(result.archive)).toBe('node-v22.23.1-darwin-x64.tar.gz');
    expect(path.basename(result.archive)).not.toBe(path.basename(nodePinFor(pins, 'darwin-arm64').source));
  });
});

// ---------------------------------------------------------------------------
// Nothing is pinned twice.
//
// A digest written down in two places drifts, and the copy that drifts is the
// one nobody updates. Test files are allowed to hold a digest -- that is what an
// independent witness IS, a value obtained some other way and compared -- so the
// rule is about the files that BUILD: the workflows, the scripts, the package
// manifest. For those there is exactly one place a digest may live, and it is
// resource-pins.json.
// ---------------------------------------------------------------------------

const repoRoot = execFileSync('git', ['rev-parse', '--show-toplevel'], { cwd: path.resolve('.'), encoding: 'utf8' }).trim();

function pinnedDigests(): string[] {
  const found = new Set<string>();
  const walk = (value: unknown) => {
    if (typeof value === 'string') { if (/^[a-f0-9]{64}$/.test(value)) found.add(value); return; }
    if (Array.isArray(value)) { value.forEach(walk); return; }
    if (value && typeof value === 'object') Object.values(value as Record<string, unknown>).forEach(walk);
  };
  walk(pins);
  return [...found];
}

function buildingFiles(): { file: string; text: string }[] {
  return execFileSync('git', ['ls-files', '-z', '--', '.github/workflows', 'desktop/scripts', 'desktop/package.json'], { cwd: repoRoot, encoding: 'utf8' })
    .split('\0')
    .filter(Boolean)
    .map((file) => ({ file, text: readFileSync(path.join(repoRoot, file), 'utf8') }));
}

describe('every pinned digest lives in exactly one place', () => {
  it('appears in resource-pins.json and in no other file that builds', () => {
    const files = buildingFiles();
    const duplicated = pinnedDigests().flatMap((digest) => files
      .filter(({ file, text }) => !file.endsWith('resource-pins.json') && text.includes(digest))
      .map(({ file }) => `${digest.slice(0, 12)}... also in ${file}`));
    expect(duplicated.join('\n') || 'each digest written down once').toBe('each digest written down once');
  });

  it('holds the Intel Node digests, and holds them only there', () => {
    // Named outright, because the way this rule gets broken is by a workflow
    // that fetches the new archive with the digest typed into its `run:` block.
    // desktop-windows-app.yml faced the same problem and solved it by reading
    // every URL and digest out of resource-pins.json as the step runs; that this
    // stays true of it is pinned in windows-app-artifact-workflow.test.ts, "does
    // not retype a pinned digest that resource-pins.json already holds".
    const text = readFileSync(path.join(repoRoot, 'desktop/scripts/resource-pins.json'), 'utf8');
    const intel = ['b8da981b8a0b1241b70249204916da76c63573ddf5814dbd2d1e41069105cb81', '03afb3618a2685335209c93f8c34633f8316dbe6cc32196bc19daa1a73852e5b'];
    expect(intel.filter((digest) => text.includes(digest))).toEqual(intel);
    const elsewhere = buildingFiles()
      .filter(({ file }) => !file.endsWith('resource-pins.json'))
      .filter(({ text: body }) => intel.some((digest) => body.includes(digest)))
      .map(({ file }) => file);
    expect(elsewhere.join(', ') || 'only in the pin file').toBe('only in the pin file');
  });
});

// ---------------------------------------------------------------------------
// A binary for another architecture is checked by its hash, never by running it.
//
// assertNodePin hashes the staged executable and then runs it with --version.
// On a cross build that second step is the whole failure: `Bad CPU type in
// executable`, from inside `npm run assemble`, on a runner. The pin knows which
// architecture it describes, so the decision is taken from the pin -- no caller
// has to remember to pass a flag, and no caller can forget to.
//
// The hash check is what does not move. It is the only thing standing between
// the pin and the bytes, and a pin that skipped it for foreign architectures
// would ship whatever happened to be in the cache.
// ---------------------------------------------------------------------------

const host = `${process.platform}-${process.arch}`;

describe('a foreign-architecture Node pin is verified without being executed', () => {
  it('accepts bytes matching the pin without running them', () => {
    // The file written here is text, not a Mach-O binary. If anything tries to
    // execute it the test fails with an exec error rather than a hash error,
    // which is exactly the distinction being drawn.
    const file = path.join(temp(), 'node');
    const bytes = 'not a Mach-O binary, and never executed';
    writeFileSync(file, bytes);
    const foreign = host === 'darwin-x64' ? 'darwin-arm64' : 'darwin-x64';
    const pin = { ...nodePinFor(pins, foreign), executableSha256: sha(bytes) };
    return expect(assembly.assertNodePin(file, pin)).resolves.toBeUndefined();
  });

  it('still refuses bytes that do not match the pin', () => {
    const file = path.join(temp(), 'node');
    writeFileSync(file, 'tampered');
    const foreign = host === 'darwin-x64' ? 'darwin-arm64' : 'darwin-x64';
    return expect(assembly.assertNodePin(file, nodePinFor(pins, foreign))).rejects.toThrow('executable hash mismatch');
  });

  it('still runs the version check when the pin is for this machine', () => {
    // The native path is unchanged: the same bytes, hashed and then asked what
    // version they are. Text pretending to be node fails the exec, and that is
    // the proof the probe is still there.
    const file = path.join(temp(), 'node');
    const bytes = 'not a Mach-O binary either';
    writeFileSync(file, bytes, { mode: 0o755 });
    const native = { ...nodePinFor(pins, 'darwin-arm64'), platform: host, executableSha256: sha(bytes) };
    return expect(assembly.assertNodePin(file, native)).rejects.toThrow();
  });
});

// ---------------------------------------------------------------------------
// What a target architecture means to the build.
//
// Three separate vocabularies meet here and none of them agree. Node and
// electron-builder say `x64`; Go says `amd64`; the runtime manifest and
// src/main/resources.ts say `darwin-x64`. Translating between them by hand at
// each call site is how a build ends up passing GOARCH=x64 to a toolchain that
// silently has no such target, or labelling an Intel bundle arm64.
// ---------------------------------------------------------------------------

describe('the assembly target', () => {
  it('translates an Intel target into the arch Go understands', () => {
    expect(assemblyTarget('darwin-x64', 'darwin-arm64')).toEqual({
      platform: 'darwin-x64', goos: 'darwin', goarch: 'amd64', native: false,
    });
  });

  it('leaves the Apple silicon target exactly as it builds today', () => {
    expect(assemblyTarget('darwin-arm64', 'darwin-arm64')).toEqual({
      platform: 'darwin-arm64', goos: 'darwin', goarch: 'arm64', native: true,
    });
  });

  it('calls an arm64 target on an Intel Mac foreign too, in both directions', () => {
    // Symmetry matters: someone on an Intel MacBook building the arm64 bundle is
    // in the same position as the runner, and must not be allowed to probe
    // either. Rosetta running one of these two directions is not a promise
    // anything here may rely on.
    expect(assemblyTarget('darwin-arm64', 'darwin-x64').native).toBe(false);
  });

  it('refuses a target it has no pin for', () => {
    expect(() => assemblyTarget('darwin-ppc', 'darwin-arm64')).toThrow('darwin-ppc');
  });

  it('refuses to build a macOS bundle from somewhere that is not macOS', () => {
    // Cross-ARCHITECTURE is what this adds. Cross-OS is not: the assembly runs
    // ditto, reads a .app layout and stages a Mach-O runtime.
    // The message has to name both, or "it threw" is satisfied by any accident
    // -- a typo, a missing export -- and the rule proves nothing.
    expect(() => assemblyTarget('darwin-x64', 'win32-x64')).toThrow(/darwin-x64[^]*win32-x64|win32-x64[^]*darwin-x64/);
  });
});

// ---------------------------------------------------------------------------
// Where the manifest's vc version comes from.
//
// assemble-resources.mjs runs the vc it just built with --version and writes the
// answer into the manifest. For a cross build that binary cannot run here. The
// version string is not architecture-dependent -- it is the ldflags value, or
// "dev" when there are none -- so the answer is available from a build that DOES
// run here, and the plan below says so outright: ship the target build, ask the
// host build.
//
// The native plan is one build and one probe, unchanged, which is what keeps
// today's arm64 manifest byte for byte what it is.
// ---------------------------------------------------------------------------

describe('the vc builds a target needs', () => {
  it('is one build, probed in place, when the target is this machine', () => {
    expect(vcBuildPlan(assemblyTarget('darwin-arm64', 'darwin-arm64'), 'darwin-arm64')).toEqual([
      { goos: 'darwin', goarch: 'arm64', purpose: 'ship' },
    ]);
  });

  it('is the shipped build plus a host build to ask the version of, when it is not', () => {
    expect(vcBuildPlan(assemblyTarget('darwin-x64', 'darwin-arm64'), 'darwin-arm64')).toEqual([
      { goos: 'darwin', goarch: 'amd64', purpose: 'ship' },
      { goos: 'darwin', goarch: 'arm64', purpose: 'version' },
    ]);
  });

  it('never plans to probe the binary it is shipping when that binary is foreign', () => {
    const plan = vcBuildPlan(assemblyTarget('darwin-x64', 'darwin-arm64'), 'darwin-arm64');
    const shipped = plan.filter((build) => build.purpose === 'ship');
    const probed = plan.filter((build) => build.purpose === 'version');
    expect(probed.every((build) => build.goarch !== shipped[0].goarch)).toBe(true);
    expect(probed.map((build) => build.goarch)).toEqual(['arm64']);
  });
});

// ---------------------------------------------------------------------------
// The npm version in the manifest, read rather than asked for.
//
// The third and last probe. The manifest records the private npm's version by
// running `staging/node/bin/npm --version`. That path is a symlink to a script
// whose shebang is `#!/usr/bin/env node`, and the assembly hands it a PATH whose
// first entry is `staging/node/bin` -- so the node that runs it is the STAGED
// node, the foreign one. It fails for the same reason the other two do, less
// obviously.
//
// npm's version is written in npm's own package.json, inside the distribution
// that was just authenticated by digest. Reading it there costs nothing, works
// for any architecture, and cannot disagree with what the executable would have
// said: they are the same file's `version` field, one read and one printed.
// ---------------------------------------------------------------------------

describe('the private npm version', () => {
  it('is read out of the staged distribution', () => {
    const staging = temp();
    const npmRoot = path.join(staging, 'node/lib/node_modules/npm');
    mkdirSync(npmRoot, { recursive: true });
    writeFileSync(path.join(npmRoot, 'package.json'), JSON.stringify({ name: 'npm', version: '10.9.8' }));
    expect(stagedNpmVersion(staging)).toBe('10.9.8');
  });

  it('says which tree it could not find a version in', () => {
    const staging = temp();
    let thrown = 'nothing was thrown for a staging tree with no npm in it';
    try { stagedNpmVersion(staging); } catch (error) { thrown = (error as Error).message; }
    expect(thrown).toContain(staging);
  });
});

// ---------------------------------------------------------------------------
// The assembly no longer has one architecture written into it.
//
// A rule read off the file rather than run. It cannot prove a cross build works;
// it proves the constants that make one impossible are gone -- the hardcoded
// GOARCH, the constant platform stamped on the manifest, the refusal to start
// anywhere but an arm64 Mac. Every one of those is a literal, and a literal is a
// thing a reader can check. What replaces them is the target the caller asks
// for, resolved by assemblyTarget above.
// ---------------------------------------------------------------------------

describe('scripts/assemble-resources.mjs takes its architecture from outside', () => {
  it('names no architecture as a constant', () => {
    const script = readFileSync(new URL('../scripts/assemble-resources.mjs', import.meta.url), 'utf8');
    const code = script.split('\n').filter((line) => !/^\s*(?:\/\/|\*|\/\*)/.test(line)).join('\n');
    const literals = [...code.matchAll(/(['"`])(?:darwin-arm64|darwin-x64|arm64|amd64|x64)\1/g)].map((match) => match[0]);
    expect(literals.join(', ') || 'no architecture written into the script').toBe('no architecture written into the script');
  });
});

// ---------------------------------------------------------------------------
// The packaged app on an Intel Mac.
//
// src/main/resources.ts:94 works out which runtime manifest it will accept from
// process.platform alone: anything that is not win32 must say `darwin-arm64`. On
// an Intel Mac that refuses the app's own private runtime, and the build would
// have been for nothing -- an artifact that downloads, installs and will not
// start. The architecture belongs in that answer.
// ---------------------------------------------------------------------------

describe('the private runtime a machine will accept', () => {
  it('is the manifest written for that machine, architecture included', () => {
    expect(expectedRuntimePlatform('darwin', 'x64')).toBe('darwin-x64');
    expect(expectedRuntimePlatform('darwin', 'arm64')).toBe('darwin-arm64');
    expect(expectedRuntimePlatform('win32', 'x64')).toBe('win32-x64');
  });

  it('refuses to name a platform nothing is built for', () => {
    // Silently answering something for linux-arm64 would turn "we do not build
    // that" into "the manifest does not match", which sends the reader looking
    // in the wrong place.
    expect(() => expectedRuntimePlatform('linux', 'arm64')).toThrow(/linux/);
  });

  it('agrees with what the assembly would stamp on a bundle for this machine', () => {
    // One name for one machine, decided in one place. The manifest the assembly
    // writes and the manifest the app accepts have to be the same string, or
    // every bundle is rejected by the app that shipped it.
    expect(expectedRuntimePlatform(process.platform, process.arch)).toBe(assemblyTarget(host, host).platform);
  });
});

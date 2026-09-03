import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { lstat, mkdir, readFile, readdir, realpath, rename, rm } from 'node:fs/promises';
import path from 'node:path';

// The three vocabularies that meet in a cross build, written down once. Node and
// electron-builder say `x64`, Go says `amd64`, and the runtime manifest says
// `${process.platform}-${process.arch}` -- which is the key resource-pins.json
// is indexed by, and the name src/main/resources.ts compares a manifest
// against. Translating between them at each call site is how a build ends up
// passing a GOARCH no toolchain has, or labelling an Intel bundle arm64.
const ASSEMBLY_PLATFORMS = {
  'darwin-arm64': { goos: 'darwin', goarch: 'arm64' },
  'darwin-x64': { goos: 'darwin', goarch: 'amd64' },
  'win32-x64': { goos: 'windows', goarch: 'amd64' },
};

/**
 * The Node pin for one platform, carrying the platform it is for.
 *
 * The lookup is data: a platform this file has never heard of is a new entry in
 * resource-pins.json, not a new branch here. Everything downstream -- the cache
 * fetcher, the assembly, the archive name -- asks this way, and the platform
 * travelling with the pin is what lets `assertNodePin` decide whether the bytes
 * it just hashed can also be run.
 */
export function nodePinFor(file, platform) {
  const platforms = file?.platforms ?? {};
  const pinned = platforms[platform]?.node;
  if (pinned === undefined) {
    const known = Object.keys(platforms).sort().join(', ') || 'no platform at all';
    throw new Error(`no private Node pin for ${platform}; resource-pins.json pins ${known}`);
  }
  return { ...pinned, platform };
}

/**
 * What building `target` from `host` means, in every vocabulary at once.
 *
 * `native` is the whole point: it is false in BOTH directions between the two
 * Mac architectures, because Rosetta is not a promise a runner image makes and
 * nothing here may rely on one. A false `native` is what stops the assembly
 * learning anything by running what it just produced.
 */
export function assemblyTarget(target, host) {
  const known = Object.keys(ASSEMBLY_PLATFORMS).sort().join(', ');
  for (const platform of [target, host]) {
    if (!Object.hasOwn(ASSEMBLY_PLATFORMS, platform)) throw new Error(`resource assembly has no target called ${platform}; it builds ${known}`);
  }
  const operatingSystem = (platform) => platform.split('-')[0];
  if (operatingSystem(target) !== operatingSystem(host)) {
    throw new Error(`resource assembly cannot build ${target} on ${host}: the assembly stages a native runtime and reads a bundle layout, so target and host must be the same operating system`);
  }
  return { platform: target, ...ASSEMBLY_PLATFORMS[target], native: target === host };
}

/**
 * The vc builds one target needs, and what each is for.
 *
 * Native: one build, shipped, and asked its version in place -- unchanged, which
 * is what keeps today's manifest byte for byte what it is. Foreign: the shipped
 * build cannot run here, so the version comes from a second build for the host.
 * The version string is the ldflags value (or "dev"), which does not vary with
 * the architecture, so the two builds cannot disagree about it.
 */
export function vcBuildPlan(target, host) {
  const ship = { goos: target.goos, goarch: target.goarch, purpose: 'ship' };
  if (target.native) return [ship];
  const runnable = assemblyTarget(host, host);
  return [ship, { goos: runnable.goos, goarch: runnable.goarch, purpose: 'version' }];
}

/**
 * The private npm's version, read out of the distribution rather than asked of
 * it.
 *
 * `staging/node/bin/npm` is a script whose shebang finds the STAGED node, so
 * running it is running the foreign binary. The number it would print is the
 * `version` field of the very package.json read here, inside a distribution
 * already authenticated by digest.
 */
export function stagedNpmVersion(stagingRoot) {
  const manifest = path.join(stagingRoot, 'node/lib/node_modules/npm/package.json');
  let version;
  try {
    version = JSON.parse(readFileSync(manifest, 'utf8')).version;
  } catch (error) {
    throw new Error(`cannot read the version of the staged private npm from ${manifest}: ${error.message}`);
  }
  if (typeof version !== 'string' || version === '') throw new Error(`the staged private npm at ${manifest} declares no version`);
  return version;
}

export async function shaFile(file) {
  return createHash('sha256').update(await readFile(file)).digest('hex');
}

export async function treeHash(root) {
  const hash = createHash('sha256');
  const visit = async (directory) => {
    for (const entry of (await readdir(directory, { withFileTypes: true })).sort((a, b) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0)) {
      const absolute = path.join(directory, entry.name);
      const relative = path.relative(root, absolute).split(path.sep).join('/');
      if (entry.isDirectory()) await visit(absolute);
      else hash.update(relative).update('\0').update(await readFile(absolute)).update('\0');
    }
  };
  await visit(root);
  return hash.digest('hex');
}

async function assertMissing(destination) {
  try {
    await lstat(destination);
    throw new Error(`refusing to overwrite hoisted dependency: ${destination}`);
  } catch (error) {
    if (error?.code !== 'ENOENT') throw error;
  }
}

export async function hoistPiBundledDependencies(piRoot) {
  const bundled = path.join(piRoot, 'node_modules/@earendil-works/pi-coding-agent/node_modules');
  const target = path.join(piRoot, 'node_modules');
  for (const entry of await readdir(bundled, { withFileTypes: true })) {
    const source = path.join(bundled, entry.name);
    if (entry.name === '.bin') {
      await rm(source, { recursive: true });
    } else if (entry.name.startsWith('@')) {
      const targetScope = path.join(target, entry.name);
      await mkdir(targetScope, { recursive: true });
      for (const child of await readdir(source, { withFileTypes: true })) {
        const destination = path.join(targetScope, child.name);
        await assertMissing(destination);
        await rename(path.join(source, child.name), destination);
      }
      await rm(source, { recursive: true });
    } else {
      const destination = path.join(target, entry.name);
      await assertMissing(destination);
      await rename(source, destination);
    }
  }
  await rm(bundled, { recursive: true });
}

export async function assertWindowsInstallablePaths(root) {
  const longestLocalUser = '12345678901234567890';
  const installRoot = `C:\\Users\\${longestLocalUser}\\AppData\\Local\\Programs\\Void Code\\resources\\private-runtime`;
  const visit = async (directory) => {
    for (const entry of await readdir(directory, { withFileTypes: true })) {
      const absolute = path.join(directory, entry.name);
      const relative = path.relative(root, absolute).split(path.sep).join('\\');
      const installed = `${installRoot}\\${relative}`;
      if (installed.length >= 260) throw new Error(`Windows installer path limit exceeded (${installed.length}): ${relative}`);
      if (entry.isDirectory()) await visit(absolute);
    }
  };
  await visit(root);
}

export function expectedNodeArchive(pin) {
  if (!/^v\d+\.\d+\.\d+$/.test(pin.version)) throw new Error('invalid private Node version pin');
  // The platform comes from the pin, so a new entry that kept the old entry's
  // URL is refused here rather than fetched, hashed against the old digest and
  // staged inside a bundle labelled something else.
  const archiveName = `node-${pin.version}-${pin.platform}.tar.gz`;
  const source = `https://nodejs.org/dist/${pin.version}/${archiveName}`;
  if (pin.source !== source) throw new Error(`private Node source identifier mismatch: ${pin.source}`);
  if (!/^[a-f0-9]{64}$/.test(pin.sourceArchiveSha256)) throw new Error('invalid private Node archive hash pin');
  return { archiveName, root: archiveName.slice(0, -'.tar.gz'.length), source };
}

export async function extractPinnedNodeArchive(archivePath, destination, pin) {
  const { root } = expectedNodeArchive(pin);
  const archiveHash = await shaFile(archivePath);
  if (archiveHash !== pin.sourceArchiveSha256) throw new Error(`private Node archive hash mismatch: ${archiveHash}`);

  const members = execFileSync('tar', ['-tzf', archivePath], { encoding: 'utf8', maxBuffer: 32 * 1024 * 1024 }).trimEnd().split('\n');
  const executableMember = `${root}/bin/node`;
  let executableCount = 0;
  for (const member of members) {
    if (!member || member.includes('\\') || path.posix.isAbsolute(member)) throw new Error(`unsafe private Node archive member: ${member}`);
    const parts = member.replace(/\/$/, '').split('/');
    if (parts.some((part) => part === '' || part === '.' || part === '..') || parts[0] !== root) {
      throw new Error(`unsafe private Node archive member: ${member}`);
    }
    if (member === executableMember) executableCount += 1;
  }
  if (executableCount !== 1) throw new Error(`unexpected private Node archive layout: executable count ${executableCount}`);

  await mkdir(destination, { recursive: true });
  // The authenticated Node distribution is also the source of the private npm
  // needed by vc's managed extension reconciliation. Extract only after every
  // archive member has passed the traversal check above.
  execFileSync('tar', ['-xzf', archivePath, '-C', destination]);
  const executable = path.join(destination, executableMember);
  const metadata = await lstat(executable);
  if (!metadata.isFile() || metadata.isSymbolicLink()) throw new Error('unexpected private Node archive executable type');
  const resolvedDestination = `${await realpath(destination)}${path.sep}`;
  if (!(await realpath(executable)).startsWith(resolvedDestination)) throw new Error('unsafe private Node archive executable path');
  await assertNodePin(executable, pin);
  return executable;
}

export async function assertNodePin(nodePath, pin) {
  expectedNodeArchive(pin);
  // The hash is what stands between the pin and the bytes, and it is checked for
  // every architecture. Only the version probe below is conditional.
  if (await shaFile(nodePath) !== pin.executableSha256) throw new Error('private Node executable hash mismatch');
  // A binary for another architecture cannot be run here -- `Bad CPU type in
  // executable`, from inside `npm run assemble`, on a runner. The pin knows
  // which architecture it describes, so no caller has to remember to say so,
  // and none can forget to.
  if (pin.platform !== `${process.platform}-${process.arch}`) return;
  const version = execFileSync(nodePath, ['--version'], { encoding: 'utf8' }).trim();
  if (version !== pin.version) throw new Error(`private Node version mismatch: ${version}`);
}

// A pin that fails has to say what it hashed and what it saw. The bytes on disk
// are not always the bytes that were committed -- a checkout that converts line
// endings changes them -- so "hash mismatch" alone leaves the reader unable to
// tell a tampered file from a converted one without reproducing the hash by hand.
async function assertPinnedBytes(file, expected, subject) {
  const actual = await shaFile(file);
  if (actual === expected) return;
  throw new Error(`private Pi ${subject} hash mismatch: ${file} hashes to ${actual}, resource-pins.json holds ${expected}`);
}

export async function assertPiSourcePins(piSource, pin) {
  const packagePath = path.join(piSource, 'package.json');
  const lockPath = path.join(piSource, 'package-lock.json');
  await assertPinnedBytes(packagePath, pin.packageJsonSha256, 'package.json');
  await assertPinnedBytes(lockPath, pin.packageLockSha256, 'package-lock');
  const lock = JSON.parse(await readFile(lockPath, 'utf8'));
  const piPackage = lock.packages?.['node_modules/@earendil-works/pi-coding-agent'];
  if (piPackage?.version !== pin.version || piPackage?.integrity !== pin.packageIntegrity) throw new Error('private Pi lock integrity mismatch');
}

export async function assertPiTreePin(piRoot, pin) {
  const actual = await treeHash(piRoot);
  if (actual !== pin.treeSha256) throw new Error(`private Pi reconstructed tree hash mismatch: ${actual}`);
  return actual;
}

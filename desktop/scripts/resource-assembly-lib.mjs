import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { cp, lstat, mkdir, readFile, readdir, realpath, rename, rm } from 'node:fs/promises';
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

// ---------------------------------------------------------------------------
// Bundling Pi's runtime into a single file.
//
// Defender charges by the NUMBER of files, not by their size: 12,000 files of 8 KB scan in 40.5 s,
// the same 96 MB in 12 files in 7.7 s. Pi's tree is 19,069 files, and the runtime integrity check
// reads every one of them at startup. Bundled, the tree is 17 files, and a cold Windows start went
// from 278-280 s to 11.6 s -- measured on the Windows machine, not extrapolated. Pruning the tree by
// extension was the earlier, smaller step (134.8 s) and remains the way back.
//
// The entry point is dist/bun/cli.js, not dist/cli.js. That is Pi's own entry for being built into a
// single artifact: it statically registers bedrock and the OAuth flows, which pi-ai deliberately
// hides from bundlers behind variable specifiers -- "so bundlers cannot follow the import", in its
// own words. Bundled through dist/cli.js instead, Pi dies the first time a provider is used.
//
// The price is in the file name. Pi's extension loader, outside bun mode, builds jiti's aliases with
// require.resolve("typebox") and friends, eagerly, before any extension asks for anything -- so with
// no node_modules on disk it kills EVERY extension, including the transport extension vc installs,
// which is how the app gets a provider at all. The one mode where Pi serves extensions from the
// artifact itself (virtualModules) is selected by isBunBinary, and that is computed from exactly one
// thing: import.meta.url.includes("$bunfs" | "~BUN" | "%7EBUN") -- config.js:16. Pi 0.84.1 has no
// flag, export or environment variable for it; its dist was grepped. Hence the name pi~BUN.mjs.
//
// Along with the extension loader, the flag switches four more branches in config.js, and that is
// the whole of the cost:
//   1. getPackageDir() moves to dirname(process.execPath) -- the directory of node.exe. Hence the
//      manifest's packageDir and PI_PACKAGE_DIR in desktop-child-env.ts (config.js checks it FIRST,
//      ahead of execPath). Without it Pi does not fail: it silently becomes version 0.0.0.
//   2. The asset layout goes flat -- theme/, assets/, export-html/ instead of dist/modes/... That is
//      exactly what Pi's own copy-binary-assets produces.
//   3. detectInstallMethod() returns 'bun-binary', so Pi's self-update is off. For a pinned runtime
//      that is what we want, but it is a change in behaviour, not nothing.
//   4. jiti is given tryNative:false. Extensions are still transpiled.
//
// Two checks stand against a Pi version bump quietly taking this away, both on the bump's own path
// in provision-pinned-pi-smoke.sh: check-pi-bun-contract.mjs reads Pi's source, and
// check-bundled-pi-smoke.mjs loads the real extension against a bundle with no node_modules.
const PI_BUNDLE_FILE = 'pi~BUN.mjs';
const PI_BUNDLE_DIR = 'agent';
// Pi's CJS dependencies (cross-spawn among them) call require() from inside an ESM bundle, and
// photon-node reads its .wasm relative to __dirname. The banner gives them both.
const PI_BUNDLE_BANNER = [
  'import{createRequire as __piCreateRequire}from"node:module";',
  'import{fileURLToPath as __piFileURLToPath}from"node:url";',
  'import{dirname as __piDirname}from"node:path";',
  'const require=__piCreateRequire(import.meta.url);',
  'const __filename=__piFileURLToPath(import.meta.url);',
  'const __dirname=__piDirname(__filename);',
].join('');
// Files Pi reads from disk at runtime rather than importing: a bundler knows nothing about them.
// Left is where the package keeps them, right is where the flat bun-binary layout looks for them.
// The list was read out of config.js rather than guessed: getThemesDir, getInteractiveAssetsDir,
// getExportTemplateDir, getPackageJsonPath, getReadmePath, getChangelogPath.
const PI_BUNDLE_ASSETS = [
  ['package.json', 'package.json'],
  ['README.md', 'README.md'],
  ['CHANGELOG.md', 'CHANGELOG.md'],
  ['dist/modes/interactive/theme/dark.json', 'theme/dark.json'],
  ['dist/modes/interactive/theme/light.json', 'theme/light.json'],
  ['dist/modes/interactive/theme/theme-schema.json', 'theme/theme-schema.json'],
  ['dist/modes/interactive/assets/clankolas.png', 'assets/clankolas.png'],
  ['dist/core/export-html/template.html', 'export-html/template.html'],
  ['dist/core/export-html/template.css', 'export-html/template.css'],
  ['dist/core/export-html/template.js', 'export-html/template.js'],
  ['dist/core/export-html/vendor/highlight.min.js', 'export-html/vendor/highlight.min.js'],
  ['dist/core/export-html/vendor/marked.min.js', 'export-html/vendor/marked.min.js'],
];
// esbuild cannot bundle native modules and should not: pi-tui loads its .node files through
// createRequire at <bundle dir>/native/... Only the target platform's module is staged -- the
// darwin ones have no business in a Windows build.
const PI_BUNDLE_NATIVE = {
  'win32-x64': [['@earendil-works/pi-tui/native/win32/prebuilds/win32-x64/win32-console-mode.node', 'native/win32/prebuilds/win32-x64/win32-console-mode.node']],
  'darwin-arm64': [['@earendil-works/pi-tui/native/darwin/prebuilds/darwin-arm64/darwin-modifiers.node', 'native/darwin/prebuilds/darwin-arm64/darwin-modifiers.node']],
  'darwin-x64': [['@earendil-works/pi-tui/native/darwin/prebuilds/darwin-x64/darwin-modifiers.node', 'native/darwin/prebuilds/darwin-x64/darwin-modifiers.node']],
};

// hoistPiBundledDependencies moves nested packages up a level, and it runs after the install, so
// the same file lives in one of two places depending on whether it has been called yet. Ask both
// rather than depend on the call order inside somebody else's script.
async function piDependencyFile(piRoot, relative) {
  const candidates = [
    path.join(piRoot, 'node_modules', relative),
    path.join(piRoot, 'node_modules/@earendil-works/pi-coding-agent/node_modules', relative),
  ];
  for (const candidate of candidates) {
    try {
      await lstat(candidate);
      return candidate;
    } catch (error) {
      if (error?.code !== 'ENOENT') throw error;
    }
  }
  throw new Error(`Pi runtime dependency is missing: ${relative}`);
}

/**
 * Collapse an installed Pi tree into one bundle plus the assets it reads from disk. Returns what the
 * manifest needs: the entry path and the package directory, both relative to `piRoot`, and the
 * package version -- read BEFORE node_modules stops existing.
 *
 * Call after assertPiSourcePins, so authenticity is checked against an untouched source, and before
 * treeHash, so the manifest describes what actually ships.
 */
export async function bundlePiRuntime(piRoot, platform) {
  const native = PI_BUNDLE_NATIVE[platform];
  if (native === undefined) throw new Error(`no Pi bundle layout for ${platform}`);
  const packageRoot = path.join(piRoot, 'node_modules/@earendil-works/pi-coding-agent');
  const version = JSON.parse(await readFile(path.join(packageRoot, 'package.json'), 'utf8')).version;
  const bundleDir = path.join(piRoot, PI_BUNDLE_DIR);
  await mkdir(bundleDir, { recursive: true });

  const { build } = await import('esbuild');
  const result = await build({
    entryPoints: [path.join(packageRoot, 'dist/bun/cli.js')],
    outfile: path.join(bundleDir, PI_BUNDLE_FILE),
    bundle: true,
    platform: 'node',
    format: 'esm',
    // This build's pinned Node, not the newest one esbuild knows about.
    target: 'node22',
    banner: { js: PI_BUNDLE_BANNER },
    logLevel: 'warning',
  });
  if (result.errors.length > 0) throw new Error(`Pi bundle failed: ${JSON.stringify(result.errors)}`);

  for (const [from, to] of PI_BUNDLE_ASSETS) {
    const destination = path.join(bundleDir, to);
    await mkdir(path.dirname(destination), { recursive: true });
    await cp(path.join(packageRoot, from), destination);
  }
  // photon-node, inside the bundle, reads its .wasm as __dirname + '/photon_rs_bg.wasm'.
  const photon = await piDependencyFile(piRoot, '@silvia-odwyer/photon-node/photon_rs_bg.wasm');
  await cp(photon, path.join(bundleDir, 'photon_rs_bg.wasm'));
  for (const [from, to] of native) {
    const destination = path.join(bundleDir, to);
    await mkdir(path.dirname(destination), { recursive: true });
    await cp(await piDependencyFile(piRoot, from), destination);
  }

  // The line the whole exercise is for.
  await rm(path.join(piRoot, 'node_modules'), { recursive: true, force: true });
  return {
    version,
    entry: `${PI_BUNDLE_DIR}/${PI_BUNDLE_FILE}`,
    packageDir: PI_BUNDLE_DIR,
  };
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

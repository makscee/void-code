'use strict';
// Проверка приватного рантайма живёт здесь, а не в resources.ts, по одной причине: её обязан
// уметь загрузить рабочий поток. Главный процесс собирается голым `tsc` в dist/main, тесты
// импортируют src/main напрямую, и node в воркере TypeScript не читает ни там, ни там — значит
// файл должен быть простым JS, лежащим по одному относительному пути в обоих деревьях
// (scripts/copy-static.mjs кладёт его в dist). Типы — рядом, в private-runtime.d.ts.
//
// Код перенесён из resources.ts дословно, скриптом: снята только разметка типов. Проверки
// целостности — сравнение lstat до и после, отказ на симлинках, побайтовый хеш дерева Pi —
// остались синхронными и в прежнем порядке. Асинхронные чтения расширили бы окно между двумя
// замерами lstat; внутри воркера окно прежней ширины.

const { createHash } = require('node:crypto');
const { lstatSync, readFileSync, readdirSync, realpathSync } = require('node:fs');
const path = require('node:path');

// Every platform a private runtime is built for. The app accepts the manifest
// written for the machine it is running on and no other: an Intel Mac that
// accepted `darwin-arm64` would be starting an arm64 runtime, and one that
// accepted nothing but `darwin-arm64` would refuse its own.
const RUNTIME_PLATFORMS = ['darwin-arm64', 'darwin-x64', 'win32-x64'];

/**
 * The manifest platform a machine will accept, architecture included.
 *
 * A platform nothing is built for is refused by name rather than answered with
 * something plausible: "we do not build that" and "the manifest does not match"
 * send the reader to different places.
 */
function expectedRuntimePlatform(platform, arch) {
  const name = `${platform}-${arch}`;
  const built = RUNTIME_PLATFORMS.find((candidate) => candidate === name);
  if (built === undefined) throw new Error(`no private runtime is built for ${name}`);
  return built;
}

// The version a build can spell: MAJOR.MINOR.PATCH with an optional prerelease,
// which is the whole range scripts/build-version.mjs emits and nothing else.
// `dev`, a leading `v` and a whole `--version` line stay refused.
const BUILD_VERSION = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/;

// What each file a manifest names is called when it is not there.
//
// Defender quarantined vc.exe out of an installed application, integrity checking correctly refused
// to start, and the person read "Unexpected startup error". That text was not carelessness:
// startupDiagnostic passes through only messages on a whitelist, and Node's ENOENT carries the full
// path of the file -- on a real installation, the account name with it. Letting it through would
// hand the diagnostic exactly what the whitelist exists to keep out.
//
// So what was missing was not the wording but a message safe enough to be on the list. These say
// what is gone and nothing about where it lived, which is what makes them listable;
// startup-diagnostic.ts takes this set rather than repeating it, so the words cannot be right in one
// file and stale in the other.
const RUNTIME_ASSETS = {
  // The coarse case, and no less likely than a single file: quarantine can take a directory, and a
  // failed installation never writes one. It arrives before any manifest is read, so none of the
  // entries below can stand in for it.
  runtime: 'The private runtime is missing',
  manifest: 'The runtime manifest is missing',
  vc: 'The vc executable is missing',
  node: 'The Node executable is missing',
  fixture: 'The round-trip fixture is missing',
  piEntry: 'The Pi entry point is missing',
  piTree: 'The Pi runtime tree is missing',
};
const MISSING_RUNTIME_ASSET_MESSAGES = Object.values(RUNTIME_ASSETS);

function within(root, candidate) {
  const relative = path.relative(root, candidate);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}
function sameFile(first, second) {
  return first.dev === second.dev && first.ino === second.ino && first.mode === second.mode && first.size === second.size && first.mtimeMs === second.mtimeMs && first.ctimeMs === second.ctimeMs;
}
function validateRelative(relative) {
  if (typeof relative !== 'string' || relative.length === 0 || path.isAbsolute(relative) || path.win32.isAbsolute(relative)) throw new Error('unsafe runtime manifest path');
  const parts = relative.split(/[\\/]/);
  if (parts.some((part) => part === '' || part === '.' || part === '..')) throw new Error('unsafe runtime manifest path');
  return parts;
}
function checkedRoot(root) {
  let before;
  try {
    before = lstatSync(root);
  } catch (error) {
    if (error.code === 'ENOENT') throw new Error(RUNTIME_ASSETS.runtime);
    throw error;
  }
  if (before.isSymbolicLink() || !before.isDirectory()) throw new Error('private runtime root must be a non-symlink directory');
  const canonical = realpathSync(root);
  const after = lstatSync(root);
  if (!sameFile(before, after) || realpathSync(root) !== canonical) throw new Error('private runtime changed during validation');
  return canonical;
}
function checkedPath(root, canonicalRoot, relative, kind, asset) {
  const parts = validateRelative(relative);
  let current = root;
  for (let index = 0; index < parts.length; index++) {
    current = path.join(current, parts[index]);
    // An absent file is a different fault from an altered one, and it has a different answer:
    // reinstall, rather than reinstall and wonder what else changed. Told apart here because this is
    // the only place that can -- every manifest-named file passes through, and by the time a caller
    // has an ENOENT the only thing left to do with it is parse it.
    let stat;
    try {
      stat = lstatSync(current);
    } catch (error) {
      if (error.code === 'ENOENT') throw new Error(RUNTIME_ASSETS[asset]);
      throw error;
    }
    if (stat.isSymbolicLink()) throw new Error('runtime assets must not be symlinks');
    const leaf = index === parts.length - 1;
    if ((!leaf || kind === 'directory') && !stat.isDirectory()) throw new Error('runtime asset has incorrect type');
    if (leaf && kind === 'file' && !stat.isFile()) throw new Error('runtime asset has incorrect type');
    if (!within(canonicalRoot, realpathSync(current))) throw new Error('runtime path escaped package resources');
  }
  return current;
}
function checkedRead(root, canonicalRoot, relative, asset) {
  const file = checkedPath(root, canonicalRoot, relative, 'file', asset);
  const before = lstatSync(file); const canonical = realpathSync(file);
  const bytes = readFileSync(file);
  const after = lstatSync(file);
  if (!sameFile(before, after) || realpathSync(file) !== canonical || !within(canonicalRoot, canonical)) throw new Error('runtime asset changed during validation');
  return bytes;
}

function sha256File(file) { return createHash('sha256').update(readFileSync(file)).digest('hex'); }
function checkedTreeSha256(root, runtimeRoot, canonicalRuntimeRoot) {
  const relativeRoot = path.relative(runtimeRoot, root).split(path.sep).join('/');
  const canonicalTreeRoot = realpathSync(checkedPath(runtimeRoot, canonicalRuntimeRoot, relativeRoot, 'directory', 'piTree'));
  const hash = createHash('sha256');
  const visit = (directory) => {
    const directoryBefore = lstatSync(directory); const directoryCanonical = realpathSync(directory);
    if (directoryBefore.isSymbolicLink() || !directoryBefore.isDirectory() || !within(canonicalTreeRoot, directoryCanonical)) throw new Error('unsupported runtime asset');
    for (const entry of readdirSync(directory, { withFileTypes: true }).sort((a, b) => a.name < b.name ? -1 : a.name > b.name ? 1 : 0)) {
      const absolute = path.join(directory, entry.name); const relative = path.relative(root, absolute).split(path.sep).join('/');
      const before = lstatSync(absolute);
      if (before.isSymbolicLink()) throw new Error(`unsupported runtime asset: ${relative}`);
      const canonical = realpathSync(absolute);
      if (!within(canonicalTreeRoot, canonical)) throw new Error(`runtime path escaped Pi tree: ${relative}`);
      if (before.isDirectory() && entry.isDirectory()) visit(absolute);
      else if (before.isFile() && entry.isFile()) {
        const bytes = readFileSync(absolute); const after = lstatSync(absolute);
        if (!sameFile(before, after) || realpathSync(absolute) !== canonical) throw new Error(`runtime asset changed during validation: ${relative}`);
        hash.update(relative).update('\0').update(bytes).update('\0');
      } else throw new Error(`unsupported runtime asset: ${relative}`);
    }
    const directoryAfter = lstatSync(directory);
    if (!sameFile(directoryBefore, directoryAfter) || realpathSync(directory) !== directoryCanonical) throw new Error('runtime tree changed during validation');
  };
  visit(root);
  return hash.digest('hex');
}
function treeSha256(root) {
  const parent = path.dirname(root); const canonicalParent = checkedRoot(parent);
  return checkedTreeSha256(root, parent, canonicalParent);
}
function resolvePrivateRuntime(root) {
  if (root.includes('app.asar')) throw new Error('private executables must be outside asar');
  const canonicalRoot = checkedRoot(root);
  const manifest = JSON.parse(checkedRead(root, canonicalRoot, 'manifest.json', 'manifest').toString('utf8'));
  const expectedPlatform = expectedRuntimePlatform(process.platform, process.arch);
  if (manifest.schema !== 1 || manifest.platform !== expectedPlatform) throw new Error('unsupported private runtime manifest');
  // Required, not optional. An optional field is one an assembly can quietly
  // stop writing, which is exactly what happened to the vc version stamp: a
  // runtime that cannot say which build it is does not start.
  if (typeof manifest.build?.version !== 'string' || !BUILD_VERSION.test(manifest.build.version) || typeof manifest.build.describe !== 'string' || manifest.build.describe.trim() === '') throw new Error('private runtime manifest records no build version');
  const vc = checkedPath(root, canonicalRoot, manifest.vc.path, 'file', 'vc');
  const node = checkedPath(root, canonicalRoot, manifest.node.path, 'file', 'node');
  const fixture = checkedPath(root, canonicalRoot, manifest.fixture.path, 'file', 'fixture');
  const piRoot = checkedPath(root, canonicalRoot, 'pi', 'directory', 'piTree');
  const piEntry = checkedPath(root, canonicalRoot, manifest.pi.entry, 'file', 'piEntry');
  if (!within(realpathSync(piRoot), realpathSync(piEntry))) throw new Error('Pi entrypoint escaped Pi tree');
  // Checked the same way entry is: inside the Pi tree, not a symlink, a directory. A package
  // directory that does not contain its own entry point is somebody else's directory, and Pi would
  // read somebody else's package.json and themes out of it.
  let piPackageDir;
  if (manifest.pi.packageDir !== undefined) {
    piPackageDir = checkedPath(root, canonicalRoot, manifest.pi.packageDir, 'directory', 'piTree');
    if (!within(realpathSync(piRoot), realpathSync(piPackageDir))) throw new Error('Pi package directory escaped Pi tree');
    if (!within(realpathSync(piPackageDir), realpathSync(piEntry))) throw new Error('Pi entrypoint escaped its package directory');
  }
  if (createHash('sha256').update(checkedRead(root, canonicalRoot, manifest.vc.path, 'vc')).digest('hex') !== manifest.vc.sha256) throw new Error('vc resource hash mismatch');
  if (createHash('sha256').update(checkedRead(root, canonicalRoot, manifest.node.path, 'node')).digest('hex') !== manifest.node.sha256) throw new Error('Node resource hash mismatch');
  if (createHash('sha256').update(checkedRead(root, canonicalRoot, manifest.fixture.path, 'fixture')).digest('hex') !== manifest.fixture.sha256) throw new Error('fixture resource hash mismatch');
  if (checkedTreeSha256(piRoot, root, canonicalRoot) !== manifest.pi.treeSha256) throw new Error('Pi resource hash mismatch');
  for (const [relative, expected, asset] of [[manifest.vc.path, vc, 'vc'], [manifest.node.path, node, 'node'], [manifest.fixture.path, fixture, 'fixture'], [manifest.pi.entry, piEntry, 'piEntry']]) {
    if (checkedPath(root, canonicalRoot, relative, 'file', asset) !== expected) throw new Error('runtime asset changed during validation');
  }
  checkedPath(root, canonicalRoot, 'pi', 'directory', 'piTree'); checkedRoot(root);
  return { root, vc, node, piEntry, piPackageDir, fixture, manifest };
}

module.exports = { RUNTIME_PLATFORMS, MISSING_RUNTIME_ASSET_MESSAGES, expectedRuntimePlatform, sha256File, treeSha256, resolvePrivateRuntime };

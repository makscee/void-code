import { createHash } from 'node:crypto';
import { lstatSync, readFileSync, readdirSync, realpathSync, type Stats } from 'node:fs';
import path from 'node:path';

export interface RuntimeManifest {
  schema: 1;
  platform: 'darwin-arm64' | 'win32-x64';
  vc: { version: string; sourceCommit: string; path: string; sha256: string };
  node: { version: string; path: string; sha256: string };
  pi: { version: string; entry: string; treeSha256: string };
  fixture: { path: string; sha256: string };
}
export interface PrivateRuntime { root: string; vc: string; node: string; piEntry: string; fixture: string; manifest: RuntimeManifest }

function within(root: string, candidate: string): boolean {
  const relative = path.relative(root, candidate);
  return relative === '' || (!path.isAbsolute(relative) && relative !== '..' && !relative.startsWith(`..${path.sep}`));
}
function sameFile(first: Stats, second: Stats): boolean {
  return first.dev === second.dev && first.ino === second.ino && first.mode === second.mode && first.size === second.size && first.mtimeMs === second.mtimeMs && first.ctimeMs === second.ctimeMs;
}
function validateRelative(relative: string): string[] {
  if (typeof relative !== 'string' || relative.length === 0 || path.isAbsolute(relative) || path.win32.isAbsolute(relative)) throw new Error('unsafe runtime manifest path');
  const parts = relative.split(/[\\/]/);
  if (parts.some((part) => part === '' || part === '.' || part === '..')) throw new Error('unsafe runtime manifest path');
  return parts;
}
function checkedRoot(root: string): string {
  const before = lstatSync(root);
  if (before.isSymbolicLink() || !before.isDirectory()) throw new Error('private runtime root must be a non-symlink directory');
  const canonical = realpathSync(root);
  const after = lstatSync(root);
  if (!sameFile(before, after) || realpathSync(root) !== canonical) throw new Error('private runtime changed during validation');
  return canonical;
}
function checkedPath(root: string, canonicalRoot: string, relative: string, kind: 'file' | 'directory'): string {
  const parts = validateRelative(relative);
  let current = root;
  for (let index = 0; index < parts.length; index++) {
    current = path.join(current, parts[index]);
    const stat = lstatSync(current);
    if (stat.isSymbolicLink()) throw new Error('runtime assets must not be symlinks');
    const leaf = index === parts.length - 1;
    if ((!leaf || kind === 'directory') && !stat.isDirectory()) throw new Error('runtime asset has incorrect type');
    if (leaf && kind === 'file' && !stat.isFile()) throw new Error('runtime asset has incorrect type');
    if (!within(canonicalRoot, realpathSync(current))) throw new Error('runtime path escaped package resources');
  }
  return current;
}
function checkedRead(root: string, canonicalRoot: string, relative: string): Buffer {
  const file = checkedPath(root, canonicalRoot, relative, 'file');
  const before = lstatSync(file); const canonical = realpathSync(file);
  const bytes = readFileSync(file);
  const after = lstatSync(file);
  if (!sameFile(before, after) || realpathSync(file) !== canonical || !within(canonicalRoot, canonical)) throw new Error('runtime asset changed during validation');
  return bytes;
}

export function sha256File(file: string): string { return createHash('sha256').update(readFileSync(file)).digest('hex'); }
function checkedTreeSha256(root: string, runtimeRoot: string, canonicalRuntimeRoot: string): string {
  const relativeRoot = path.relative(runtimeRoot, root).split(path.sep).join('/');
  const canonicalTreeRoot = realpathSync(checkedPath(runtimeRoot, canonicalRuntimeRoot, relativeRoot, 'directory'));
  const hash = createHash('sha256');
  const visit = (directory: string): void => {
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
export function treeSha256(root: string): string {
  const parent = path.dirname(root); const canonicalParent = checkedRoot(parent);
  return checkedTreeSha256(root, parent, canonicalParent);
}
export function resolvePrivateRuntime(root: string): PrivateRuntime {
  if (root.includes('app.asar')) throw new Error('private executables must be outside asar');
  const canonicalRoot = checkedRoot(root);
  const manifest = JSON.parse(checkedRead(root, canonicalRoot, 'manifest.json').toString('utf8')) as RuntimeManifest;
  const expectedPlatform = process.platform === 'win32' ? 'win32-x64' : 'darwin-arm64';
  if (manifest.schema !== 1 || manifest.platform !== expectedPlatform) throw new Error('unsupported private runtime manifest');
  const vc = checkedPath(root, canonicalRoot, manifest.vc.path, 'file');
  const node = checkedPath(root, canonicalRoot, manifest.node.path, 'file');
  const fixture = checkedPath(root, canonicalRoot, manifest.fixture.path, 'file');
  const piRoot = checkedPath(root, canonicalRoot, 'pi', 'directory');
  const piEntry = checkedPath(root, canonicalRoot, manifest.pi.entry, 'file');
  if (!within(realpathSync(piRoot), realpathSync(piEntry))) throw new Error('Pi entrypoint escaped Pi tree');
  if (createHash('sha256').update(checkedRead(root, canonicalRoot, manifest.vc.path)).digest('hex') !== manifest.vc.sha256) throw new Error('vc resource hash mismatch');
  if (createHash('sha256').update(checkedRead(root, canonicalRoot, manifest.node.path)).digest('hex') !== manifest.node.sha256) throw new Error('Node resource hash mismatch');
  if (createHash('sha256').update(checkedRead(root, canonicalRoot, manifest.fixture.path)).digest('hex') !== manifest.fixture.sha256) throw new Error('fixture resource hash mismatch');
  if (checkedTreeSha256(piRoot, root, canonicalRoot) !== manifest.pi.treeSha256) throw new Error('Pi resource hash mismatch');
  for (const [relative, expected] of [[manifest.vc.path, vc], [manifest.node.path, node], [manifest.fixture.path, fixture], [manifest.pi.entry, piEntry]] as const) {
    if (checkedPath(root, canonicalRoot, relative, 'file') !== expected) throw new Error('runtime asset changed during validation');
  }
  checkedPath(root, canonicalRoot, 'pi', 'directory'); checkedRoot(root);
  return { root, vc, node, piEntry, fixture, manifest };
}

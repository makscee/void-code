import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { lstat, mkdir, readFile, readdir, realpath, rename, rm } from 'node:fs/promises';
import path from 'node:path';

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
  const archiveName = `node-${pin.version}-darwin-arm64.tar.gz`;
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
  if (await shaFile(nodePath) !== pin.executableSha256) throw new Error('private Node executable hash mismatch');
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

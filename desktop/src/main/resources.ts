import { createHash } from 'node:crypto';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import path from 'node:path';

export interface RuntimeManifest {
  schema: 1;
  platform: 'darwin-arm64';
  vc: { version: string; sourceCommit: string; path: string; sha256: string };
  node: { version: string; path: string; sha256: string };
  pi: { version: string; entry: string; treeSha256: string };
  fixture: { path: string; sha256: string };
}
export interface PrivateRuntime { root: string; vc: string; node: string; piEntry: string; fixture: string; manifest: RuntimeManifest }

export function sha256File(file: string): string { return createHash('sha256').update(readFileSync(file)).digest('hex'); }
export function treeSha256(root: string): string {
  const hash = createHash('sha256');
  const visit = (directory: string): void => {
    for (const name of readdirSync(directory).sort()) {
      const absolute = path.join(directory, name);
      const relative = path.relative(root, absolute).split(path.sep).join('/');
      const stat = statSync(absolute);
      if (stat.isDirectory()) visit(absolute);
      else if (stat.isFile()) hash.update(relative).update('\0').update(readFileSync(absolute)).update('\0');
      else throw new Error(`unsupported runtime asset: ${relative}`);
    }
  };
  visit(root);
  return hash.digest('hex');
}
function safe(root: string, relative: string): string {
  if (path.isAbsolute(relative) || relative.includes('..')) throw new Error('unsafe runtime manifest path');
  const resolved = path.resolve(root, relative);
  if (!resolved.startsWith(`${path.resolve(root)}${path.sep}`)) throw new Error('runtime path escaped package resources');
  return resolved;
}
export function resolvePrivateRuntime(root: string): PrivateRuntime {
  if (root.includes('app.asar')) throw new Error('private executables must be outside asar');
  const manifest = JSON.parse(readFileSync(path.join(root, 'manifest.json'), 'utf8')) as RuntimeManifest;
  if (manifest.schema !== 1 || manifest.platform !== 'darwin-arm64') throw new Error('unsupported private runtime manifest');
  const vc = safe(root, manifest.vc.path);
  const node = safe(root, manifest.node.path);
  const piEntry = safe(root, manifest.pi.entry);
  const fixture = safe(root, manifest.fixture.path);
  if (sha256File(vc) !== manifest.vc.sha256) throw new Error('vc resource hash mismatch');
  if (sha256File(node) !== manifest.node.sha256) throw new Error('Node resource hash mismatch');
  if (sha256File(fixture) !== manifest.fixture.sha256) throw new Error('fixture resource hash mismatch');
  const piRoot = path.join(root, 'pi');
  if (treeSha256(piRoot) !== manifest.pi.treeSha256) throw new Error('Pi resource hash mismatch');
  if (!statSync(piEntry).isFile()) throw new Error('Pi entrypoint unavailable');
  return { root, vc, node, piEntry, fixture, manifest };
}

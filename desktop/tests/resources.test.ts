import { chmodSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { resolvePrivateRuntime, sha256File, treeSha256 } from '../src/main/resources';

const roots: string[] = [];
function fixture(): string {
  const root = path.join(os.tmpdir(), `private-runtime-${crypto.randomUUID()}`); roots.push(root);
  mkdirSync(path.join(root, 'vc/bin'), { recursive: true }); mkdirSync(path.join(root, 'node/bin'), { recursive: true }); mkdirSync(path.join(root, 'pi'), { recursive: true }); mkdirSync(path.join(root, 'fixture'), { recursive: true });
  for (const file of ['vc/bin/vc', 'node/bin/node', 'pi/cli.js', 'fixture/test.js']) { writeFileSync(path.join(root, file), file); chmodSync(path.join(root, file), 0o755); }
  const manifest = { schema: 1, platform: process.platform === 'win32' ? 'win32-x64' : 'darwin-arm64', build: { version: '0.2.50', describe: 'v0.2.50' }, vc: { version: 'v', sourceCommit: 'c', path: 'vc/bin/vc', sha256: sha256File(path.join(root, 'vc/bin/vc')) }, node: { version: 'v', path: 'node/bin/node', sha256: sha256File(path.join(root, 'node/bin/node')) }, pi: { version: 'v', entry: 'pi/cli.js', treeSha256: treeSha256(path.join(root, 'pi')) }, fixture: { path: 'fixture/test.js', sha256: sha256File(path.join(root, 'fixture/test.js')) } };
  writeFileSync(path.join(root, 'manifest.json'), JSON.stringify(manifest)); return root;
}
function withManifest(root: string, change: (manifest: Record<string, unknown>) => void): string {
  const file = path.join(root, 'manifest.json');
  const manifest = JSON.parse(readFileSync(file, 'utf8')) as Record<string, unknown>;
  change(manifest); writeFileSync(file, JSON.stringify(manifest)); return root;
}
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

describe('private runtime manifest', () => {
  it('resolves only manifest-owned files without PATH lookup', () => { const root = fixture(); const runtime = resolvePrivateRuntime(root); expect(runtime.node).toBe(path.join(root, 'node/bin/node')); });
  it('rejects changed assets and path escape', () => { const root = fixture(); writeFileSync(path.join(root, 'node/bin/node'), 'changed'); expect(() => resolvePrivateRuntime(root)).toThrow('hash mismatch'); });
  it('rejects resources inside asar', () => { expect(() => resolvePrivateRuntime('/tmp/app.asar/private-runtime')).toThrow('outside asar'); });
  it('produces a stable tree hash independent of file creation order', () => { const first = fixture(); const second = fixture(); expect(treeSha256(path.join(first, 'pi'))).toBe(treeSha256(path.join(second, 'pi'))); expect(readFileSync(path.join(first, 'pi/cli.js'), 'utf8')).toBe('pi/cli.js'); });
  for (const [name, target] of [['manifest', 'manifest.json'], ['vc', 'vc/bin/vc'], ['node', 'node/bin/node'], ['fixture', 'fixture/test.js'], ['Pi entry', 'pi/cli.js']] as const) {
    it(`rejects a symlinked ${name}`, () => {
      const root = fixture(); const original = path.join(root, `${name.replaceAll(' ', '-')}-original`); const file = path.join(root, target);
      writeFileSync(original, readFileSync(file)); rmSync(file); symlinkSync(original, file, 'file');
      expect(() => resolvePrivateRuntime(root)).toThrow(/symlink|manifest/);
    });
  }
  it('rejects symlinked ancestors and every link in the Pi tree', () => {
    const root = fixture(); const bin = path.join(root, 'node/bin'); const realBin = path.join(root, 'real-bin');
    mkdirSync(realBin); writeFileSync(path.join(realBin, 'node'), 'node/bin/node'); rmSync(bin, { recursive: true }); symlinkSync(realBin, bin, 'dir');
    expect(() => resolvePrivateRuntime(root)).toThrow('symlink');
    const second = fixture(); const outside = path.join(second, 'outside.js'); writeFileSync(outside, 'outside'); symlinkSync(outside, path.join(second, 'pi/nested.js'), 'file');
    expect(() => resolvePrivateRuntime(second)).toThrow('unsupported runtime asset');
    const third = fixture(); const outsideDir = path.join(third, 'outside-dir'); mkdirSync(outsideDir); writeFileSync(path.join(outsideDir, 'x'), 'x'); symlinkSync(outsideDir, path.join(third, 'pi/nested'), 'dir');
    expect(() => resolvePrivateRuntime(third)).toThrow('unsupported runtime asset');
  });
  it('rejects structurally unsafe manifest paths', () => {
    for (const unsafe of ['../vc', 'vc/../bin/vc', 'vc//bin/vc', './vc/bin/vc', 'C:\\outside']) {
      const root = fixture(); const manifest = JSON.parse(readFileSync(path.join(root, 'manifest.json'), 'utf8')); manifest.vc.path = unsafe; writeFileSync(path.join(root, 'manifest.json'), JSON.stringify(manifest));
      expect(() => resolvePrivateRuntime(root)).toThrow('unsafe runtime manifest path');
    }
  });
  // -------------------------------------------------------------------------
  // The manifest says which BUILD this runtime came out of, not only which
  // versions of vc, Node and Pi are inside it.
  //
  // Everything else in the manifest is about the parts. The question a person
  // with a problem is asked -- "which version have you got?" -- is about the
  // whole, and until now nothing in the packaged application could answer it:
  // the app read 0.1.0 out of a package.json that had not changed since July.
  //
  // It is REQUIRED rather than optional on purpose. An optional field is one an
  // assembly can quietly stop writing -- which is precisely what happened to
  // the vc version stamp, silently, in a change that was correct about
  // everything else. A runtime that cannot say which build it is does not
  // start.
  // -------------------------------------------------------------------------
  it('surfaces the version of the build that assembled it', () => {
    const runtime = resolvePrivateRuntime(fixture());
    expect(runtime.manifest.build).toEqual({ version: '0.2.50', describe: 'v0.2.50' });
  });

  it('refuses a runtime that cannot say which build it is', () => {
    for (const build of [undefined, {}, { describe: 'v0.2.50' }, { version: '', describe: '' }, { version: 'dev', describe: 'dev' }, { version: 'v0.2.50', describe: 'v0.2.50' }, { version: 42, describe: 'v0.2.50' }]) {
      const root = withManifest(fixture(), (manifest) => { if (build === undefined) delete manifest.build; else manifest.build = build; });
      expect(() => resolvePrivateRuntime(root), `${JSON.stringify(build)} was accepted as a build identity`).toThrow();
    }
  });

  it('accepts a build off the tag, because a branch build is a real build', () => {
    const root = withManifest(fixture(), (manifest) => { manifest.build = { version: '0.2.50-3-gabc1234', describe: 'v0.2.50-3-gabc1234' }; });
    expect(resolvePrivateRuntime(root).manifest.build.version).toBe('0.2.50-3-gabc1234');
  });

  it('preserves npm links outside the trusted Pi tree', () => {
    const root = fixture(); symlinkSync('node', path.join(root, 'node/bin/npm'), 'file'); symlinkSync('node', path.join(root, 'node/bin/npx'), 'file');
    expect(resolvePrivateRuntime(root).node).toBe(path.join(root, 'node/bin/node'));
  });
});

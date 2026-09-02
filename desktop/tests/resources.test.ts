// rails:pin-on-coverage the four containment tests at the foot of this file pin guards that already work, so nothing can go red; each was proved by deleting its guard from resources.ts and watching exactly one test fail -- except `runtime path escaped Pi tree`, which survives deletion and is reported as unreachable rather than covered
import { chmodSync, mkdirSync, readFileSync, rmSync, symlinkSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { resolvePrivateRuntime, sha256File, treeSha256 } from '../src/main/resources';
import { startupDiagnostic, startupDialogMessage } from '../src/main/startup-diagnostic';

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

// ---------------------------------------------------------------------------
// Defender quarantined vc.exe out of an installed application on a live machine -- five
// Trojan:Win32/Bearfoos.A!ml detections in seven hours, one of them inside the installer as it
// unpacked. Integrity checking then did its job: the tree no longer matched the manifest and the app
// refused to start. What the person saw was "Unexpected startup error".
//
// That text is not carelessness, and it took reproducing the whole chain to see why: startupDiagnostic
// passes through only messages on a whitelist, and Node's ENOENT carries the full path of the file.
// Letting it through would hand the diagnostic exactly what the whitelist exists to keep out. So the
// hole is not the wording -- it is that no safe message for "the file is gone" exists to put on the
// list.
//
// checkedPath is where it belongs: every file a manifest names passes through it, and it is the only
// place that can tell "not there" from "contents differ". A fixed string with the kind of asset and
// no place on disk is safe by construction, which is what makes it listable.
// ---------------------------------------------------------------------------
describe('a runtime asset that is gone is reported as gone', () => {
  for (const [asset, target, names] of [
    ['the runtime manifest', 'manifest.json', /manifest/i],
    ['vc', 'vc/bin/vc', /\bvc\b/i],
    ['Node', 'node/bin/node', /node/i],
    ['the round-trip fixture', 'fixture/test.js', /fixture/i],
    ['the Pi entry point', 'pi/cli.js', /pi/i],
    ['the Pi tree', 'pi', /pi/i],
  ] as const) {
    it(`says ${asset} is missing, without saying where it was`, () => {
      const root = fixture();
      rmSync(path.join(root, target), { recursive: true, force: true });

      let thrown: Error | undefined;
      try { resolvePrivateRuntime(root); } catch (error) { thrown = error as Error; }
      expect(thrown, `a missing ${asset} was accepted`).toBeInstanceOf(Error);
      const message = thrown?.message ?? '';

      // The load-bearing half. Node's own ENOENT reads "ENOENT: no such file or directory, lstat
      // '/var/folders/.../pi/cli.js'" -- a whole path, including the user's account name on a real
      // installation. A message with nothing locational in it is one the whitelist can carry.
      expect(message, 'the refusal carries a path, so it cannot be let through the diagnostic whitelist').not.toMatch(/[\\/]/);
      expect(message, 'the refusal is a raw filesystem error').not.toContain('ENOENT');
      expect(message, 'the refusal names the temporary root it happened to run in').not.toContain(root);
      expect(message, 'the refusal repeats the manifest-relative path').not.toContain(target);

      // The other half: it has to say what is wrong and about what, or it is no better than the
      // "Unexpected startup error" it replaces.
      expect(message, 'the refusal does not say the asset is missing').toMatch(/missing|absent|not found|gone/i);
      expect(message, `the refusal does not say it was ${asset}`).toMatch(names);

      // And the point of all of it: this message reaches the person. A refusal that the whitelist
      // still swallows leaves the app saying "Unexpected startup error" exactly as it does today,
      // and this whole change would be invisible from outside.
      const diagnostic = startupDiagnostic('runtime-validation', thrown, '0.1.0', '2026-09-03T00:00:00.000Z');
      expect(diagnostic.error.message, 'the refusal is not on the diagnostic whitelist, so the person still sees "Unexpected startup error"').toBe(message);
    });
  }

  it('says so when the whole private runtime is gone, not just one file out of it', () => {
    // The coarser half of the same fault, and no less likely: quarantine can take a directory, and a
    // failed install never writes one. It arrives at a different check -- checkedRoot, before any
    // manifest is read -- so nothing the six cases above pin covers it, and the raw ENOENT comes out
    // with the path exactly as it used to for a single file.
    const root = fixture();
    rmSync(root, { recursive: true, force: true });

    let thrown: Error | undefined;
    try { resolvePrivateRuntime(root); } catch (error) { thrown = error as Error; }
    expect(thrown, 'a runtime that is not there at all was accepted').toBeInstanceOf(Error);
    const message = thrown?.message ?? '';

    expect(message, 'the refusal carries a path, so it cannot be let through the diagnostic whitelist').not.toMatch(/[\\/]/);
    expect(message, 'the refusal is a raw filesystem error').not.toContain('ENOENT');
    expect(message, 'the refusal names the temporary root it happened to run in').not.toContain(root);
    expect(message, 'the refusal does not say anything is missing').toMatch(/missing|absent|not found|gone/i);
    expect(message, 'the refusal does not say what was missing').toMatch(/runtime/i);

    const diagnostic = startupDiagnostic('runtime-validation', thrown, '0.1.0', '2026-09-03T00:00:00.000Z');
    expect(diagnostic.error.message, 'the refusal is not on the diagnostic whitelist, so the person still sees "Unexpected startup error"').toBe(message);
    expect(startupDialogMessage(diagnostic), 'the dialog does not tell the person a file is missing').toMatch(/missing|absent|not found|gone/i);
  });

  it('tells the person their file is missing and what to do about it', async () => {
    // The end of the chain, built from the real error rather than from a string typed here: the
    // dialog is the only part of this a person ever reads, and pinning it against an invented
    // message would prove the test agrees with itself.
    const root = fixture();
    rmSync(path.join(root, 'vc/bin/vc'));
    let thrown: Error | undefined;
    try { resolvePrivateRuntime(root); } catch (error) { thrown = error as Error; }
    const text = startupDialogMessage(startupDiagnostic('runtime-validation', thrown, '0.1.0', '2026-09-03T00:00:00.000Z'));

    expect(text, 'the dialog does not say a file is missing').toMatch(/missing|absent|not found|gone/i);
    expect(text, 'the dialog does not say what to do about it').toMatch(/reinstall|install again/i);
    expect(text, 'the dialog leaks a path').not.toMatch(/[\\/]/);

    // The implementer's caveat, and it is right: a missing file is not proof of an antivirus. A bad
    // install and a disk cleaner do the same thing, and the advice is the same either way -- so if
    // the text names a culprit at all, it has to name it as the likely one and not as the fact.
    if (/antivirus|anti-virus|defender|security software/i.test(text)) {
      expect(text, 'the dialog states an antivirus removed the file as though it knew').toMatch(/usually|often|most commonly|typically|may |might |can be|sometimes|likely/i);
    }
  });

  it('still tells a changed file apart from a missing one', () => {
    // Missing and tampered are different faults with different answers -- reinstall against
    // reinstall-and-suspect-the-disk -- and collapsing them into one message would be a cheaper way
    // to pass everything above.
    const root = fixture();
    writeFileSync(path.join(root, 'node/bin/node'), 'changed');
    expect(() => resolvePrivateRuntime(root)).toThrow('Node resource hash mismatch');
  });
});

// ---------------------------------------------------------------------------
// The manifest names a Pi package directory, and that name leaves this process: it becomes
// PI_PACKAGE_DIR in the environment of the child that runs Pi. A manifest is data read off disk, so
// the guards that keep that name inside the Pi tree are the boundary between a tampered file and a
// directory of somebody else's choosing -- Pi would read its package.json, its themes and its
// settings out of whatever it was pointed at.
//
// Three of the four had no test at all. They are checked here the way every other refusal in this
// file is: a real tree in a temporary directory and a manifest edited to point somewhere it should
// not reach. Nothing is mocked and no Electron is involved.
// ---------------------------------------------------------------------------
describe('a manifest cannot point Pi out of its own tree', () => {
  it('accepts a package directory that holds the entry point, and hands it back', () => {
    // First, so that the refusals below are refusals of something specific rather than of
    // everything: a well-formed packageDir is accepted and reaches the caller.
    const root = withManifest(fixture(), (manifest) => { (manifest.pi as Record<string, unknown>).packageDir = 'pi'; });
    expect(resolvePrivateRuntime(root).piPackageDir).toBe(path.join(root, 'pi'));
  });

  it('refuses a package directory that leaves the Pi tree', () => {
    // 'vc' is a real directory of this fixture, not a symlink and not a traversal, so every earlier
    // check passes it: it is exactly the shape a hostile manifest would use. What refuses it is the
    // containment check and nothing else.
    const root = withManifest(fixture(), (manifest) => { (manifest.pi as Record<string, unknown>).packageDir = 'vc'; });
    expect(() => resolvePrivateRuntime(root)).toThrow('Pi package directory escaped Pi tree');
  });

  it('refuses a package directory that does not contain the entry point it is paired with', () => {
    // Both inside the Pi tree, so the previous guard is satisfied and this one is the only thing
    // standing. A package directory without its own entry point is somebody else's directory.
    const root = fixture();
    mkdirSync(path.join(root, 'pi/agent'));
    writeFileSync(path.join(root, 'pi/agent/keep.js'), 'keep');
    const manifest = JSON.parse(readFileSync(path.join(root, 'manifest.json'), 'utf8')) as { pi: Record<string, unknown> };
    manifest.pi.packageDir = 'pi/agent';
    manifest.pi.treeSha256 = treeSha256(path.join(root, 'pi'));
    writeFileSync(path.join(root, 'manifest.json'), JSON.stringify(manifest));
    expect(() => resolvePrivateRuntime(root)).toThrow('Pi entrypoint escaped its package directory');
  });

  it('refuses an entry point outside the Pi tree, package directory or no package directory', () => {
    // The older guard beside the two new ones, uncovered until now for the same reason: it was
    // added without a test. 'fixture/test.js' is a real file this manifest already names elsewhere.
    for (const change of [
      (manifest: Record<string, unknown>) => { (manifest.pi as Record<string, unknown>).entry = 'fixture/test.js'; },
      (manifest: Record<string, unknown>) => { const pi = manifest.pi as Record<string, unknown>; pi.entry = 'fixture/test.js'; pi.packageDir = 'pi'; },
    ]) {
      expect(() => resolvePrivateRuntime(withManifest(fixture(), change))).toThrow('Pi entrypoint escaped Pi tree');
    }
  });
});

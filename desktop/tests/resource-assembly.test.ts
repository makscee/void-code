import { execFileSync } from 'node:child_process';
import { appendFile, cp, mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { assertNodePin, assertPiSourcePins, assertPiTreePin, assertWindowsInstallablePaths, expectedNodeArchive, extractPinnedNodeArchive, hoistPiBundledDependencies, shaFile, treeHash } from '../scripts/resource-assembly-lib.mjs';
import pins from '../scripts/resource-pins.json';

const temporary: string[] = [];
async function temp() {
  const directory = await mkdtemp(path.join(os.tmpdir(), 'vc12-pins-'));
  temporary.push(directory);
  return directory;
}
afterEach(async () => { await Promise.all(temporary.splice(0).map((directory) => rm(directory, { recursive: true, force: true }))); });

const archive = path.resolve('runtime/cache/node', expectedNodeArchive(pins.node).archiveName);

describe('resource source pins', () => {
  it.skipIf(process.platform === 'win32')('extracts authenticated Node and its private npm from the exact pinned official archive', async () => {
    const destination = await temp();
    const executable = await extractPinnedNodeArchive(archive, destination, pins.node);
    await expect(assertNodePin(executable, pins.node)).resolves.toBeUndefined();
    const npm = path.join(path.dirname(executable), 'npm');
    expect(execFileSync(npm, ['--version'], { encoding: 'utf8', env: { PATH: `${path.dirname(executable)}:/usr/bin:/bin` } }).trim()).toBe('10.9.8');

    const changed = path.join(await temp(), 'node');
    await cp(executable, changed);
    await appendFile(changed, 'tampered');
    await expect(assertNodePin(changed, pins.node)).rejects.toThrow('Node executable hash mismatch');
    const wrongVersion = { ...pins.node, version: 'v22.23.2', source: 'https://nodejs.org/dist/v22.23.2/node-v22.23.2-darwin-arm64.tar.gz' };
    await expect(assertNodePin(executable, wrongVersion)).rejects.toThrow('Node version mismatch');
  });

  it('rejects attacker source and all-zero archive metadata', () => {
    const attacked = { ...pins.node, source: 'https://attacker.invalid/node.tar.gz', sourceArchiveSha256: '0'.repeat(64) };
    expect(() => expectedNodeArchive(attacked)).toThrow('Node source identifier mismatch');
  });

  it('rejects a missing Node archive', async () => {
    await expect(extractPinnedNodeArchive(path.join(await temp(), 'missing.tar.gz'), await temp(), pins.node)).rejects.toThrow();
  });

  it.skipIf(process.platform === 'win32')('rejects a tampered Node archive before extraction', async () => {
    const changed = path.join(await temp(), 'node.tar.gz');
    await cp(archive, changed);
    await appendFile(changed, 'tampered');
    await expect(extractPinnedNodeArchive(changed, await temp(), pins.node)).rejects.toThrow('Node archive hash mismatch');
  });

  it.skipIf(process.platform === 'win32')('rejects traversal and unexpected archive layouts', async () => {
    const traversal = path.join(await temp(), 'traversal.tar.gz');
    execFileSync('python3', ['-c', "import io,sys,tarfile; t=tarfile.open(sys.argv[1],'w:gz'); i=tarfile.TarInfo('../escape'); i.size=1; t.addfile(i,io.BytesIO(b'x')); t.close()", traversal]);
    await expect(extractPinnedNodeArchive(traversal, await temp(), { ...pins.node, sourceArchiveSha256: await shaFile(traversal) })).rejects.toThrow('unsafe private Node archive member');

    const work = await temp();
    const root = expectedNodeArchive(pins.node).root;
    await mkdir(path.join(work, root), { recursive: true });
    await writeFile(path.join(work, root, 'README.md'), 'wrong layout');
    const unexpected = path.join(await temp(), 'unexpected.tar.gz');
    execFileSync('tar', ['-czf', unexpected, '-C', work, root]);
    await expect(extractPinnedNodeArchive(unexpected, await temp(), { ...pins.node, sourceArchiveSha256: await shaFile(unexpected) })).rejects.toThrow('unexpected private Node archive layout');
  });

  it('rejects a tampered Pi lock before installation', async () => {
    const source = path.resolve('runtime/pi');
    await expect(assertPiSourcePins(source, pins.pi)).resolves.toBeUndefined();
    const changed = await temp();
    await cp(source, changed, { recursive: true, filter: (entry) => !entry.includes('node_modules') });
    await writeFile(path.join(changed, 'package-lock.json'), `${await readFile(path.join(changed, 'package-lock.json'), 'utf8')} `);
    await expect(assertPiSourcePins(changed, pins.pi)).rejects.toThrow('package-lock hash mismatch');
  });

  it('hoists bundled Pi dependencies into NSIS-installable paths without changing their bytes', async () => {
    const tree = await temp();
    const nested = path.join(tree, 'node_modules/@earendil-works/pi-coding-agent/node_modules/@mistralai/mistralai');
    const longName = `${'workflow'.repeat(12)}.js`;
    await mkdir(nested, { recursive: true });
    await writeFile(path.join(nested, longName), 'runtime bytes');
    await mkdir(path.join(tree, 'node_modules/.bin'), { recursive: true });
    await writeFile(path.join(tree, 'node_modules/.bin/pi'), 'root shim');
    await mkdir(path.join(tree, 'node_modules/@earendil-works/pi-coding-agent/node_modules/.bin'), { recursive: true });
    await writeFile(path.join(tree, 'node_modules/@earendil-works/pi-coding-agent/node_modules/.bin/transitive'), 'nested shim');

    await expect(assertWindowsInstallablePaths(tree)).rejects.toThrow('Windows installer path limit');
    await hoistPiBundledDependencies(tree);

    const hoisted = path.join(tree, 'node_modules/@mistralai/mistralai', longName);
    expect(await readFile(hoisted, 'utf8')).toBe('runtime bytes');
    expect(await readFile(path.join(tree, 'node_modules/.bin/pi'), 'utf8')).toBe('root shim');
    await expect(readFile(path.join(tree, 'node_modules/.bin/transitive'))).rejects.toThrow();
    await expect(assertWindowsInstallablePaths(tree)).resolves.toBeUndefined();
  });

  it('rejects a Pi tree changed after reconstruction', async () => {
    const tree = await temp();
    await writeFile(path.join(tree, 'entry.js'), 'pinned');
    const localPin = { treeSha256: await treeHash(tree) };
    await expect(assertPiTreePin(tree, localPin)).resolves.toBe(localPin.treeSha256);
    await writeFile(path.join(tree, 'entry.js'), 'tampered');
    await expect(assertPiTreePin(tree, localPin)).rejects.toThrow('reconstructed tree hash mismatch');
    expect(await shaFile(path.join(tree, 'entry.js'))).not.toBe(localPin.treeSha256);
  });
});

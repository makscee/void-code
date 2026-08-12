import { cp, chmod, lstat, mkdir, mkdtemp, readFile, readdir, readlink, rename, rm, symlink, writeFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { assertPiSourcePins, assertPiTreePin, expectedNodeArchive, extractPinnedNodeArchive, shaFile, treeHash } from './resource-assembly-lib.mjs';

async function materializeTreeLinks(root) {
  for (const entry of await readdir(root, { withFileTypes: true })) {
    const absolute = path.join(root, entry.name);
    if (entry.isDirectory()) await materializeTreeLinks(absolute);
    else if (entry.isSymbolicLink()) {
      const target = path.resolve(path.dirname(absolute), await readlink(absolute));
      const targetStat = await lstat(target);
      await rm(absolute);
      await cp(target, absolute, { recursive: targetStat.isDirectory() });
    }
  }
}

const desktop = process.cwd();
const repo = path.resolve(desktop, '..');
const resources = path.join(desktop, 'resources');
const output = path.join(resources, 'staged');
const expectedCommit = 'd68c24b0af8f9bce3824bde4b0ab6077f40a40c1';
const pins = JSON.parse(await readFile(path.join(desktop, 'scripts/resource-pins.json'), 'utf8'));
if (process.platform !== 'darwin' || process.arch !== 'arm64') throw new Error('resource assembly supports only macOS-arm64');
if (pins.schema !== 1 || pins.platform !== 'darwin-arm64') throw new Error('unsupported resource pins');
const commit = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repo, encoding: 'utf8' }).trim();
try { execFileSync('git', ['merge-base', '--is-ancestor', expectedCommit, commit], { cwd: repo, stdio: 'ignore' }); }
catch { throw new Error(`VC-15 source commit is not an ancestor of ${commit}`); }

// Inputs are authenticated before any package manifest can bless them.
const nodeArchive = expectedNodeArchive(pins.node);
const nodeArchivePath = path.join(desktop, 'runtime/cache/node', nodeArchive.archiveName);
const piSource = process.env.VOID_DESKTOP_PI_SOURCE ?? path.join(desktop, 'runtime/pi');
await assertPiSourcePins(piSource, pins.pi);

const nodeExtraction = await mkdtemp(path.join(desktop, '.node-extraction-'));
let nodeSource;
try {
  nodeSource = await extractPinnedNodeArchive(nodeArchivePath, nodeExtraction, pins.node);
} catch (error) {
  await rm(nodeExtraction, { recursive: true, force: true });
  throw error;
}

await mkdir(resources, { recursive: true });
const staging = await mkdtemp(path.join(resources, '.assembly-'));
try {
  await mkdir(path.join(staging, 'vc/bin'), { recursive: true });
  await mkdir(path.join(staging, 'node/bin'), { recursive: true });
  await mkdir(path.join(staging, 'fixture'), { recursive: true });
  await mkdir(path.join(staging, 'pi'), { recursive: true });

  const vcPath = path.join(staging, 'vc/bin/vc');
  execFileSync('go', ['build', '-trimpath', '-buildvcs=false', '-o', vcPath, './cmd/vc'], { cwd: repo, env: { ...process.env, CGO_ENABLED: '0', GOOS: 'darwin', GOARCH: 'arm64' }, stdio: 'inherit' });
  const nodePath = path.join(staging, 'node/bin/node');
  const nodeDistribution = path.resolve(nodeSource, '../..');
  await cp(nodeSource, nodePath);
  await symlink('../lib/node_modules/npm/bin/npm-cli.js', path.join(staging, 'node/bin/npm'));
  await symlink('../lib/node_modules/npm/bin/npx-cli.js', path.join(staging, 'node/bin/npx'));
  await mkdir(path.join(staging, 'node/lib/node_modules'), { recursive: true });
  await cp(path.join(nodeDistribution, 'lib/node_modules/npm'), path.join(staging, 'node/lib/node_modules/npm'), { recursive: true });
  await chmod(nodePath, 0o755);
  await chmod(vcPath, 0o755);

  await cp(path.join(piSource, 'package.json'), path.join(staging, 'pi/package.json'));
  await cp(path.join(piSource, 'package-lock.json'), path.join(staging, 'pi/package-lock.json'));
  execFileSync('npm', ['ci', '--offline', '--ignore-scripts', '--no-audit', '--no-fund'], {
    cwd: path.join(staging, 'pi'),
    env: process.env,
    stdio: 'inherit',
  });
  // npm's generated hidden lock varies with npm itself; the source lock remains pinned.
  await rm(path.join(staging, 'pi/node_modules/.package-lock.json'), { force: true });
  // Pi is a trusted executable tree: materialize npm's convenience links so the
  // shipped tree contains only real directories and regular files.
  await materializeTreeLinks(path.join(staging, 'pi'));
  const piTreeSha256 = await assertPiTreePin(path.join(staging, 'pi'), pins.pi);

  await cp(path.join(desktop, 'dist/fixture/round-trip.js'), path.join(staging, 'fixture/round-trip.js'));
  const piPackage = JSON.parse(await readFile(path.join(staging, 'pi/node_modules/@earendil-works/pi-coding-agent/package.json'), 'utf8'));
  const manifest = {
    schema: 1,
    platform: 'darwin-arm64',
    vc: { version: execFileSync(vcPath, ['--version'], { encoding: 'utf8' }).trim(), sourceCommit: expectedCommit, path: 'vc/bin/vc', sha256: await shaFile(vcPath) },
    node: {
      version: pins.node.version,
      source: nodeArchive.source,
      sourceArchiveSha256: pins.node.sourceArchiveSha256,
      path: 'node/bin/node',
      sha256: pins.node.executableSha256,
      npm: {
        version: execFileSync(path.join(staging, 'node/bin/npm'), ['--version'], { encoding: 'utf8', env: { ...process.env, PATH: `${path.join(staging, 'node/bin')}:/usr/bin:/bin` } }).trim(),
        treeSha256: await treeHash(path.join(staging, 'node/lib/node_modules/npm')),
      },
    },
    pi: { version: piPackage.version, entry: 'pi/node_modules/@earendil-works/pi-coding-agent/dist/cli.js', treeSha256: piTreeSha256 },
    fixture: { path: 'fixture/round-trip.js', sha256: await shaFile(path.join(staging, 'fixture/round-trip.js')) },
  };
  await writeFile(path.join(staging, 'manifest.json'), `${JSON.stringify(manifest, null, 2)}\n`, { mode: 0o644 });
  await rm(output, { recursive: true, force: true });
  await rename(staging, output);
  console.log(JSON.stringify(manifest));
} catch (error) {
  await rm(staging, { recursive: true, force: true });
  throw error;
} finally {
  await rm(nodeExtraction, { recursive: true, force: true });
}

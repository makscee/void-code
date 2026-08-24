import { cp, chmod, lstat, mkdir, mkdtemp, readFile, readdir, readlink, rename, rm, symlink, writeFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { assemblyTarget, assertPiSourcePins, assertPiTreePin, expectedNodeArchive, extractPinnedNodeArchive, nodePinFor, shaFile, stagedNpmVersion, treeHash, vcBuildPlan } from './resource-assembly-lib.mjs';

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
if (pins.schema !== 1) throw new Error('unsupported resource pins');
// The architecture comes from outside: whatever the caller asks for, defaulting
// to this machine's. Which platforms exist, and whether this one can build that
// one at all, is assemblyTarget's answer -- not a constant here.
const host = `${process.platform}-${process.arch}`;
const target = assemblyTarget(`${process.platform}-${process.env.VOID_DESKTOP_MAC_ARCH ?? process.arch}`, host);
const commit = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repo, encoding: 'utf8' }).trim();
try { execFileSync('git', ['merge-base', '--is-ancestor', expectedCommit, commit], { cwd: repo, stdio: 'ignore' }); }
catch { throw new Error(`VC-15 source commit is not an ancestor of ${commit}`); }

// Inputs are authenticated before any package manifest can bless them.
//
// Two pinned Node distributions, and they are the same one unless the build is
// aimed elsewhere: the SHIPPED distribution is the target's, and the RUNNABLE
// one is this machine's. Reconstructing Pi means running npm, and a Node built
// for another architecture cannot run here -- so the work is done by the
// runnable distribution and the shipped one takes its place before anything is
// written down about it. Both are authenticated by digest either way.
const nodePin = nodePinFor(pins, target.platform);
const nodeArchive = expectedNodeArchive(nodePin);
const runnableNodePin = target.native ? nodePin : nodePinFor(pins, host);
const cachedArchive = (pin) => path.join(desktop, 'runtime/cache/node', expectedNodeArchive(pin).archiveName);
const piSource = process.env.VOID_DESKTOP_PI_SOURCE ?? path.join(desktop, 'runtime/pi');
await assertPiSourcePins(piSource, pins.pi);

// One extraction directory for both: each official archive unpacks under its
// own versioned, platform-named root, so they cannot collide.
const nodeExtraction = await mkdtemp(path.join(desktop, '.node-extraction-'));
let runnableNodeSource;
let nodeSource;
try {
  runnableNodeSource = await extractPinnedNodeArchive(cachedArchive(runnableNodePin), nodeExtraction, runnableNodePin);
  nodeSource = target.native ? runnableNodeSource : await extractPinnedNodeArchive(cachedArchive(nodePin), nodeExtraction, nodePin);
} catch (error) {
  await rm(nodeExtraction, { recursive: true, force: true });
  throw error;
}

await mkdir(resources, { recursive: true });
const staging = await mkdtemp(path.join(resources, '.assembly-'));
// A build for another architecture cannot be asked its own version, so the
// answer comes from a second build for this machine. It is built outside the
// staging tree: it is a question, not a thing to ship.
const probing = await mkdtemp(path.join(desktop, '.vc-version-probe-'));
try {
  await mkdir(path.join(staging, 'vc/bin'), { recursive: true });
  await mkdir(path.join(staging, 'node/bin'), { recursive: true });
  await mkdir(path.join(staging, 'fixture'), { recursive: true });
  await mkdir(path.join(staging, 'pi'), { recursive: true });

  const vcPath = path.join(staging, 'vc/bin/vc');
  const buildVc = (destination, build) => execFileSync('go', ['build', '-trimpath', '-buildvcs=false', '-o', destination, './cmd/vc'], { cwd: repo, env: { ...process.env, CGO_ENABLED: '0', GOOS: build.goos, GOARCH: build.goarch }, stdio: 'inherit' });
  const plan = vcBuildPlan(target, host);
  buildVc(vcPath, plan.find((build) => build.purpose === 'ship'));
  const versionBuild = plan.find((build) => build.purpose === 'version');
  let vcProbePath = vcPath;
  if (versionBuild !== undefined) {
    vcProbePath = path.join(probing, 'vc');
    buildVc(vcProbePath, versionBuild);
  }
  const vcVersion = execFileSync(vcProbePath, ['--version'], { encoding: 'utf8' }).trim();

  const nodePath = path.join(staging, 'node/bin/node');
  const stagedNpmTree = path.join(staging, 'node/lib/node_modules/npm');
  const stageNodeDistribution = async (source) => {
    const distribution = path.resolve(source, '../..');
    await rm(nodePath, { force: true });
    await cp(source, nodePath);
    await rm(stagedNpmTree, { recursive: true, force: true });
    await cp(path.join(distribution, 'lib/node_modules/npm'), stagedNpmTree, { recursive: true });
    await chmod(nodePath, 0o755);
  };
  await mkdir(path.join(staging, 'node/lib/node_modules'), { recursive: true });
  await stageNodeDistribution(runnableNodeSource);
  await symlink('../lib/node_modules/npm/bin/npm-cli.js', path.join(staging, 'node/bin/npm'));
  await symlink('../lib/node_modules/npm/bin/npx-cli.js', path.join(staging, 'node/bin/npx'));
  await chmod(vcPath, 0o755);

  await cp(path.join(piSource, 'package.json'), path.join(staging, 'pi/package.json'));
  await cp(path.join(piSource, 'package-lock.json'), path.join(staging, 'pi/package-lock.json'));
  // `npm ci` output varies with the npm that runs it, and the tree is hashed
  // against a pin -- so the npm is the one just staged from the pinned Node
  // distribution, executed by that distribution's own node. Never the ambient
  // one: a cross build stages a distribution it can run for exactly this step,
  // rather than reaching for whatever node happens to be on the PATH.
  const stagedNpmCli = path.join(staging, 'node/lib/node_modules/npm/bin/npm-cli.js');
  execFileSync(nodePath, [stagedNpmCli, 'ci', '--offline', '--ignore-scripts', '--no-audit', '--no-fund'], {
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

  // Pi is reconstructed; the distribution that reconstructed it has no further
  // work to do, and the bundle ships the target's. When they are the same
  // distribution this does not happen at all.
  if (!target.native) await stageNodeDistribution(nodeSource);

  await cp(path.join(desktop, 'dist/fixture/round-trip.js'), path.join(staging, 'fixture/round-trip.js'));
  const piPackage = JSON.parse(await readFile(path.join(staging, 'pi/node_modules/@earendil-works/pi-coding-agent/package.json'), 'utf8'));
  const manifest = {
    schema: 1,
    platform: target.platform,
    vc: { version: vcVersion, sourceCommit: expectedCommit, path: 'vc/bin/vc', sha256: await shaFile(vcPath) },
    node: {
      version: nodePin.version,
      source: nodeArchive.source,
      sourceArchiveSha256: nodePin.sourceArchiveSha256,
      path: 'node/bin/node',
      sha256: nodePin.executableSha256,
      npm: {
        version: stagedNpmVersion(staging),
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
  await rm(probing, { recursive: true, force: true });
  await rm(nodeExtraction, { recursive: true, force: true });
}

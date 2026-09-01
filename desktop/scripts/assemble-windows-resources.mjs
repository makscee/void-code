import { cp, lstat, mkdir, mkdtemp, readFile, readdir, readlink, rename, rm, writeFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { assemblyTarget, assertPiSourcePins, assertWindowsInstallablePaths, hoistPiBundledDependencies, shaFile, treeHash, vcBuildPlan } from './resource-assembly-lib.mjs';
import { readBuildVersion, vcBuildArgs } from './build-version.mjs';

// The vc inside the Windows bundle is built from the tree being packaged, the
// way the macOS assembly builds its own. It used to be a file downloaded from a
// pinned past release, and the pin is what shipped v0.2.47 inside the v0.2.48
// installer: the desktop spawned `vc login --json`, a flag that release had
// never heard of, and every Windows sign-in died on `unknown flag`. A pin whose
// staleness breaks the product on every release is not a stale value, it is a
// structure -- so the structure is gone rather than refreshed.
const host = `${process.platform}-${process.arch}`;
if (host !== 'win32-x64') throw new Error(`Windows resource assembly requires win32-x64, not ${host}`);
// Which Go vocabulary this platform speaks, and which builds it needs, are
// resource-assembly-lib.mjs's answer -- never a GOOS typed into this file, which
// is how a third platform becomes a new branch here instead of a new entry in
// one map.
const target = assemblyTarget(host, host);

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
const pins = JSON.parse(await readFile(path.join(desktop, 'scripts/resource-pins.json'), 'utf8'));
const win = pins.windows;
const nodeArchive = `node-${win.node.version}-win-x64.zip`;
const nodeArchivePath = path.join(desktop, 'runtime/cache/node', nodeArchive);
if (win.node.source !== `https://nodejs.org/dist/${win.node.version}/${nodeArchive}`) throw new Error('private Windows Node source identifier mismatch');
if (await shaFile(nodeArchivePath) !== win.node.sourceArchiveSha256) throw new Error('private Windows Node archive hash mismatch');
// The bundle has to be able to say which revision its vc came from, and the
// answer is asked of git rather than written down: a constant in this file is
// what stopped describing the thing it named.
const commit = execFileSync('git', ['rev-parse', 'HEAD'], { cwd: repo, encoding: 'utf8' }).trim();
// The version this build is, from the same place the macOS assembly asks. The
// Windows installer is where the unstamped vc actually shipped.
const buildVersion = readBuildVersion();

const piSource = process.env.VOID_DESKTOP_PI_SOURCE ?? path.join(desktop, 'runtime/pi');
await assertPiSourcePins(piSource, pins.pi);
const extraction = await mkdtemp(path.join(desktop, '.node-win-extraction-'));
await mkdir(path.join(desktop, 'resources'), { recursive: true });
const staging = await mkdtemp(path.join(desktop, 'resources/.assembly-win-'));
// A build for another architecture cannot be asked its own version, so the
// answer would come from a second build for this machine. Windows packages
// itself today, so the plan asks for no such probe -- the directory is here so
// that the day it does, this is an entry in the plan and not a new branch.
const probing = await mkdtemp(path.join(desktop, '.vc-version-probe-'));
try {
  const archiveLiteral = nodeArchivePath.replaceAll("'", "''");
  const powershell = path.join(process.env.SystemRoot ?? 'C:\\Windows', 'System32/WindowsPowerShell/v1.0/powershell.exe');
  const entries = execFileSync(powershell, ['-NoProfile', '-NonInteractive', '-Command', `Add-Type -AssemblyName System.IO.Compression.FileSystem; $z=[IO.Compression.ZipFile]::OpenRead('${archiveLiteral}'); try {$z.Entries | ForEach-Object FullName} finally {$z.Dispose()}`], { encoding: 'utf8' }).split(/\r?\n/).filter(Boolean);
  const root = nodeArchive.slice(0, -4);
  if (!entries.includes(`${root}/node.exe`)) throw new Error('private Windows Node executable missing from archive');
  for (const member of entries) {
    const normalized = member.replaceAll('\\', '/');
    const parts = normalized.replace(/\/$/, '').split('/');
    if (normalized.startsWith('/') || /^[A-Za-z]:/.test(normalized) || parts.some((part) => part === '' || part === '.' || part === '..') || parts[0] !== root) throw new Error(`unsafe private Windows Node archive member: ${member}`);
  }
  execFileSync(powershell, ['-NoProfile', '-NonInteractive', '-Command', `Expand-Archive -LiteralPath '${archiveLiteral}' -DestinationPath '${extraction.replaceAll("'", "''")}' -Force`], { stdio: 'inherit' });
  const nodeSource = path.join(extraction, root);
  const nodeExe = path.join(nodeSource, 'node.exe');
  const nodeHash = await shaFile(nodeExe);
  const nodeVersion = execFileSync(nodeExe, ['--version'], { encoding: 'utf8' }).trim();
  if (nodeVersion !== win.node.version) throw new Error(`private Windows Node version mismatch: ${nodeVersion}`);

  await mkdir(path.join(staging, 'vc'), { recursive: true });
  await mkdir(path.join(staging, 'fixture'), { recursive: true });
  const stagedVc = path.join(staging, 'vc/vc.exe');
  // The argv is build-version.mjs's, ldflags included: a vc built here has to
  // be able to say which version it is, the way a release binary can.
  const buildVc = (destination, build) => execFileSync('go', vcBuildArgs(destination, buildVersion), { cwd: repo, env: { ...process.env, CGO_ENABLED: '0', GOOS: build.goos, GOARCH: build.goarch }, stdio: 'inherit' });
  const plan = vcBuildPlan(target, host);
  buildVc(stagedVc, plan.find((build) => build.purpose === 'ship'));
  const versionBuild = plan.find((build) => build.purpose === 'version');
  let vcProbePath = stagedVc;
  if (versionBuild !== undefined) {
    vcProbePath = path.join(probing, 'vc.exe');
    buildVc(vcProbePath, versionBuild);
  }
  const vcVersion = execFileSync(vcProbePath, ['--version'], { encoding: 'utf8' }).trim();

  await cp(nodeSource, path.join(staging, 'node'), { recursive: true });
  await cp(path.join(desktop, 'dist/fixture/round-trip.js'), path.join(staging, 'fixture/round-trip.js'));
  await mkdir(path.join(staging, 'pi'), { recursive: true });
  await cp(path.join(piSource, 'package.json'), path.join(staging, 'pi/package.json'));
  await cp(path.join(piSource, 'package-lock.json'), path.join(staging, 'pi/package-lock.json'));
  const privateNpm = path.join(staging, 'node/node_modules/npm/bin/npm-cli.js');
  const stagedNode = path.join(staging, 'node/node.exe');
  execFileSync(stagedNode, [privateNpm, 'ci', '--ignore-scripts', '--no-audit', '--no-fund'], {
    cwd: path.join(staging, 'pi'),
    env: { ...process.env, PATH: `${path.join(staging, 'node')};${process.env.SystemRoot}\\System32` },
    stdio: 'inherit',
  });
  await rm(path.join(staging, 'pi/node_modules/.package-lock.json'), { force: true });
  await hoistPiBundledDependencies(path.join(staging, 'pi'));

  // ПРОБА, не для слияния. Замер: Defender платит за ЧИСЛО файлов, а не за объём —
  // 12 000 мелких файлов сканируются 40.5 с, те же мегабайты в 12 файлах — 7.7 с, и
  // на мелких кеш почти не помогает (37 с при повторе против 0.2 с). В дереве Pi
  // 19 069 файлов, из них 12 800 — .ts/.mts/.cts/.map/.md, которые Node никогда не
  // исполняет, но которые проверка целостности честно читает все до одного.
  // Чистка стоит ПОСЛЕ assertPiSourcePins (пины сверены с нетронутым исходником)
  // и ДО treeHash (манифест описывает то, что реально поедет).
  const prunePatterns = [/\.m?ts$/, /\.cts$/, /\.map$/, /\.md$/, /\.markdown$/];
  // Каталоги по имени НЕ чистим. Первая проба чистила, и Pi сломался: пакет yaml
  // держит настоящий рабочий каталог dist/doc, и composer.js требует ../doc/directives.js.
  // Приложение при этом стартовало нормально и ошибок не писало — сломалось бы у
  // человека в момент первого чата. Имя каталога ничего не говорит о том, что внутри.
  let pruned = 0, prunedBytes = 0, kept = 0;
  const pruneTree = async (dir) => {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const absolute = path.join(dir, entry.name);
      if (entry.isDirectory()) await pruneTree(absolute);
      else if (prunePatterns.some((pattern) => pattern.test(entry.name))) {
        prunedBytes += (await lstat(absolute)).size;
        await rm(absolute, { force: true });
        pruned += 1;
      } else kept += 1;
    }
  };
  await pruneTree(path.join(staging, 'pi'));
  console.error(`ПРОБА: вычищено ${pruned} файлов (${Math.round(prunedBytes / 1048576)} МБ), осталось ${kept}`);
  await materializeTreeLinks(path.join(staging, 'pi'));
  await assertWindowsInstallablePaths(staging);
  const piPackage = JSON.parse(await readFile(path.join(staging, 'pi/node_modules/@earendil-works/pi-coding-agent/package.json'), 'utf8'));
  const manifest = {
    schema: 1,
    platform: target.platform,
    build: { version: buildVersion.packageVersion, describe: buildVersion.stamp },
    vc: { version: vcVersion, sourceCommit: commit, path: 'vc/vc.exe', sha256: await shaFile(stagedVc) },
    node: { version: nodeVersion, source: win.node.source, sourceArchiveSha256: win.node.sourceArchiveSha256, path: 'node/node.exe', sha256: nodeHash, npm: { version: execFileSync(stagedNode, [privateNpm, '--version'], { encoding: 'utf8' }).trim() } },
    pi: { version: piPackage.version, entry: 'pi/node_modules/@earendil-works/pi-coding-agent/dist/cli.js', sourcePackageJsonSha256: pins.pi.packageJsonSha256, sourceLockSha256: pins.pi.packageLockSha256, treeSha256: await treeHash(path.join(staging, 'pi')) },
    fixture: { path: 'fixture/round-trip.js', sha256: await shaFile(path.join(staging, 'fixture/round-trip.js')) },
  };
  await writeFile(path.join(staging, 'manifest.json'), `${JSON.stringify(manifest, null, 2)}\n`);
  const output = path.join(desktop, 'resources/staged');
  await rm(output, { recursive: true, force: true });
  await rename(staging, output);
  console.log(JSON.stringify(manifest));
} catch (error) {
  await rm(staging, { recursive: true, force: true });
  throw error;
} finally {
  await rm(probing, { recursive: true, force: true });
  await rm(extraction, { recursive: true, force: true });
}

import { cp, lstat, mkdir, mkdtemp, readFile, readdir, readlink, rename, rm, writeFile } from 'node:fs/promises';
import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { assertPiSourcePins, assertWindowsInstallablePaths, hoistPiBundledDependencies, shaFile, treeHash } from './resource-assembly-lib.mjs';

if (process.arch !== 'x64') throw new Error('Windows x64 resource assembly requires an x64 build host');
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
const pins = JSON.parse(await readFile(path.join(desktop, 'scripts/resource-pins.json'), 'utf8'));
const win = pins.windows;
const nodeArchive = `node-${win.node.version}-win-x64.zip`;
const nodeArchivePath = path.join(desktop, 'runtime/cache/node', nodeArchive);
const vcSource = path.join(desktop, 'runtime/cache/vc/vc.exe');
if (win.node.source !== `https://nodejs.org/dist/${win.node.version}/${nodeArchive}`) throw new Error('private Windows Node source identifier mismatch');
if (await shaFile(nodeArchivePath) !== win.node.sourceArchiveSha256) throw new Error('private Windows Node archive hash mismatch');
if (await shaFile(vcSource) !== win.vc.sha256) throw new Error('private Windows vc hash mismatch');

const piSource = process.env.VOID_DESKTOP_PI_SOURCE ?? path.join(desktop, 'runtime/pi');
await assertPiSourcePins(piSource, pins.pi);
const extraction = await mkdtemp(path.join(desktop, '.node-win-extraction-'));
await mkdir(path.join(desktop, 'resources'), { recursive: true });
const staging = await mkdtemp(path.join(desktop, 'resources/.assembly-win-'));
try {
  const archiveLiteral = nodeArchivePath.replaceAll("'", "''");
  const powershell = path.join(process.env.SystemRoot ?? 'C:\\Windows', 'System32/WindowsPowerShell/v1.0/powershell.exe');
  const entries = (process.platform === 'win32'
    ? execFileSync(powershell, ['-NoProfile', '-NonInteractive', '-Command', `Add-Type -AssemblyName System.IO.Compression.FileSystem; $z=[IO.Compression.ZipFile]::OpenRead('${archiveLiteral}'); try {$z.Entries | ForEach-Object FullName} finally {$z.Dispose()}`], { encoding: 'utf8' })
    : execFileSync('zipinfo', ['-1', nodeArchivePath], { encoding: 'utf8' })
  ).split(/\r?\n/).filter(Boolean);
  const root = nodeArchive.slice(0, -4);
  if (!entries.includes(`${root}/node.exe`)) throw new Error('private Windows Node executable missing from archive');
  for (const member of entries) {
    const normalized = member.replaceAll('\\', '/');
    const parts = normalized.replace(/\/$/, '').split('/');
    if (normalized.startsWith('/') || /^[A-Za-z]:/.test(normalized) || parts.some((part) => part === '' || part === '.' || part === '..') || parts[0] !== root) throw new Error(`unsafe private Windows Node archive member: ${member}`);
  }
  if (process.platform === 'win32') execFileSync(powershell, ['-NoProfile', '-NonInteractive', '-Command', `Expand-Archive -LiteralPath '${archiveLiteral}' -DestinationPath '${extraction.replaceAll("'", "''")}' -Force`], { stdio: 'inherit' });
  else execFileSync('unzip', ['-q', nodeArchivePath, '-d', extraction], { stdio: 'inherit' });
  const nodeSource = path.join(extraction, root);
  const nodeExe = path.join(nodeSource, 'node.exe');
  const nodeHash = await shaFile(nodeExe);
  const nodeVersion = win.node.version;

  await mkdir(path.join(staging, 'vc'), { recursive: true });
  await mkdir(path.join(staging, 'fixture'), { recursive: true });
  await cp(nodeSource, path.join(staging, 'node'), { recursive: true });
  await cp(vcSource, path.join(staging, 'vc/vc.exe'));
  await cp(path.join(desktop, 'dist/fixture/round-trip.js'), path.join(staging, 'fixture/round-trip.js'));
  await mkdir(path.join(staging, 'pi'), { recursive: true });
  await cp(path.join(piSource, 'package.json'), path.join(staging, 'pi/package.json'));
  await cp(path.join(piSource, 'package-lock.json'), path.join(staging, 'pi/package-lock.json'));
  const privateNpm = path.join(staging, 'node/node_modules/npm/bin/npm-cli.js');
  const stagedNode = path.join(staging, 'node/node.exe');
  const assemblyNode = process.platform === 'win32' ? stagedNode : process.execPath;
  const assemblyNpm = process.platform === 'win32' ? privateNpm : (process.env.npm_execpath ?? path.join(execFileSync('npm', ['root', '--global'], { encoding: 'utf8' }).trim(), 'npm/bin/npm-cli.js'));
  if (!assemblyNpm) throw new Error('host npm CLI is unavailable for cross-platform assembly');
  execFileSync(assemblyNode, [assemblyNpm, 'ci', '--ignore-scripts', '--no-audit', '--no-fund', '--os=win32', '--cpu=x64'], {
    cwd: path.join(staging, 'pi'),
    env: process.platform === 'win32' ? { ...process.env, PATH: `${path.join(staging, 'node')};${process.env.SystemRoot}\\System32` } : process.env,
    stdio: 'inherit',
  });
  await rm(path.join(staging, 'pi/node_modules/.package-lock.json'), { force: true });
  const clipboardRoot = path.join(staging, 'pi/node_modules/@earendil-works/pi-coding-agent/node_modules/@mariozechner');
  const clipboardEntries = (await readdir(clipboardRoot)).filter((name) => name.startsWith('clipboard-'));
  if (!clipboardEntries.includes('clipboard-win32-x64-msvc') || clipboardEntries.some((name) => name !== 'clipboard-win32-x64-msvc')) throw new Error(`private Pi target dependencies mismatch: ${clipboardEntries.join(',')}`);
  await hoistPiBundledDependencies(path.join(staging, 'pi'));
  await materializeTreeLinks(path.join(staging, 'pi'));
  await assertWindowsInstallablePaths(staging);
  const piPackage = JSON.parse(await readFile(path.join(staging, 'pi/node_modules/@earendil-works/pi-coding-agent/package.json'), 'utf8'));
  const manifest = {
    schema: 1,
    platform: 'win32-x64',
    vc: { version: process.platform === 'win32' ? execFileSync(path.join(staging, 'vc/vc.exe'), ['--version'], { encoding: 'utf8' }).trim() : win.vc.version, sourceCommit: win.vc.sourceCommit, path: 'vc/vc.exe', sha256: win.vc.sha256 },
    node: { version: nodeVersion, source: win.node.source, sourceArchiveSha256: win.node.sourceArchiveSha256, path: 'node/node.exe', sha256: nodeHash, npm: { version: JSON.parse(await readFile(path.join(staging, 'node/node_modules/npm/package.json'), 'utf8')).version } },
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
  await rm(extraction, { recursive: true, force: true });
}

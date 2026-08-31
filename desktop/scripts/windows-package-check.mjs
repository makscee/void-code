import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { treeHash } from './resource-assembly-lib.mjs';
import { assertStampedVc } from './packaged-check-lib.mjs';

if (process.platform !== 'win32' || process.arch !== 'x64') throw new Error('Windows package check requires Windows x64');

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const alive = (pid) => { try { process.kill(pid, 0); return true; } catch { return false; } };
const waitGone = async (pids) => {
  for (let attempt = 0; attempt < 50; attempt += 1) {
    if (pids.every((pid) => !alive(pid))) return;
    await sleep(100);
  }
  throw new Error(`owned process survived cleanup: ${pids.filter(alive).join(',')}`);
};
const assertSize = (observed, expected = '111x37') => {
  if (observed !== expected) throw new Error(`ConPTY child size mismatch: expected ${expected}, observed ${observed}`);
};

let temporary;
let inventory;
let processQuery;
let powershell;
let primaryError;
const activeTerminals = new Set();
const ownedPids = new Set();
const cleanupErrors = [];
const cleanupSteps = {};
const cleanupStep = async (name, action) => {
  try {
    const value = await action();
    cleanupSteps[name] = { status: 'succeeded', ...(value === undefined ? {} : { value }) };
    return value;
  } catch (error) {
    cleanupSteps[name] = { status: 'failed', error: String(error?.message ?? error) };
    cleanupErrors.push(error);
    return undefined;
  }
};

try {
  const require = createRequire(import.meta.url);
  const release = path.resolve('release');
  const unpacked = path.join(release, 'win-unpacked');
  const resources = path.join(unpacked, 'resources');
  const runtime = path.join(resources, 'private-runtime');
  const sensitivity = process.env.VC14_CHECK_SENSITIVITY;
  powershell = path.join(process.env.SystemRoot ?? 'C:\\Windows', 'System32/WindowsPowerShell/v1.0/powershell.exe');
  processQuery = `Get-CimInstance Win32_Process | Where-Object { $_.ProcessId -ne $PID -and ($_.CommandLine -like '*${runtime.replaceAll("'", "''")}*' -or $_.ExecutablePath -like '*${unpacked.replaceAll("'", "''")}*') }`;
  const inventoryScript = `${processQuery} | Select-Object ProcessId,ParentProcessId,Name,CommandLine | ConvertTo-Json -Compress`;
  let injectInventoryFailure = sensitivity === 'inventory-command-failure';
  inventory = () => {
    if (injectInventoryFailure) {
      injectInventoryFailure = false;
      throw new Error('injected inventory command failure');
    }
    return execFileSync(powershell, ['-NoProfile', '-NonInteractive', '-Command', inventoryScript], { encoding: 'utf8' }).trim();
  };
  const manifestPath = sensitivity === 'missing-manifest' ? path.join(runtime, 'missing-manifest.json') : path.join(runtime, 'manifest.json');
  const manifest = JSON.parse(await readFile(manifestPath, 'utf8'));
  const { wrapPty } = require('../dist/main/session-manager.js');
  const ptyRoot = path.join(resources, 'app.asar.unpacked/node_modules/node-pty');
  const pty = require(ptyRoot);
  const sha = async (file) => createHash('sha256').update(await readFile(file)).digest('hex');

  let sensitivityPassed = false;
  try { assertSize('110x37'); } catch { sensitivityPassed = true; }
  if (!sensitivityPassed) throw new Error('resize mismatch sensitivity check did not fail');
  const initialInventory = inventory();
  if (initialInventory) throw new Error(`dirty packaged process inventory before check: ${initialInventory}`);

  temporary = await mkdtemp(path.join(os.tmpdir(), 'Void Code ü spaced '));
  const node = path.join(runtime, manifest.node.path);
  const fixture = path.join(runtime, manifest.fixture.path);
  const runs = [];
  const run = async (mode) => {
    const terminal = wrapPty(pty.spawn(node, [fixture], { name: 'xterm-256color', cols: 80, rows: 24, cwd: temporary, useConptyDll: true, env: { SystemRoot: process.env.SystemRoot, PATH: `${process.env.SystemRoot}\\System32`, ELECTRON_RUN_AS_NODE: undefined } }));
    activeTerminals.add(terminal);
    let output = '';
    let exitCount = 0;
    let pids;
    const exited = new Promise((resolve) => terminal.onExit((event) => { exitCount += 1; activeTerminals.delete(terminal); resolve(event); }));
    terminal.onData((data) => { output += data; const match = /fixture:pids:(\d+):(\d+)/.exec(output); if (match) { pids = [Number(match[1]), Number(match[2])]; pids.forEach((pid) => ownedPids.add(pid)); } });
    const waitFor = async (pattern, label) => {
      for (let attempt = 0; attempt < 100; attempt += 1) { const match = typeof pattern === 'string' ? output.includes(pattern) : pattern.exec(output); if (match) return match; await sleep(50); }
      throw new Error(`${label} absent from PTY output: ${JSON.stringify(output)}`);
    };
    await waitFor(/fixture:ready:cwd=.*Void Code ü spaced/, 'non-ASCII spaced cwd');
    terminal.write('Привет界\r');
    await waitFor(/fixture:Привет界/, 'bidirectional Unicode bytes');
    terminal.resize(111, 37);
    await sleep(500);
    terminal.write('size\r');
    const sizeMatch = await waitFor(/fixture:size:(\d+x\d+)/, 'post-resize ConPTY child size');
    const observedSize = sizeMatch[1];
    assertSize(observedSize);
    terminal.write('tree\r');
    await waitFor(/fixture:pids:\d+:\d+/, 'descendant inventory');
    if (!pids?.every(alive)) throw new Error(`owned process tree not live before ${mode}`);
    if (mode === 'normal') terminal.write('quit\r');
    else terminal.kill();
    const exit = await Promise.race([exited, sleep(10_000).then(() => { throw new Error(`${mode} exit subscription timed out`); })]);
    await waitGone(pids);
    pids.forEach((pid) => ownedPids.delete(pid));
    if (exitCount !== 1) throw new Error(`${mode} exit subscription count ${exitCount}`);
    runs.push({ mode, cwd: temporary, unicodeRoundTrip: true, observedSize, exit, exitSubscriptions: exitCount, ownedPidsAfter: 0 });
  };

  await run('normal');
  await run('forced');
  const piPackagePath = path.join(runtime, 'pi/node_modules/@earendil-works/pi-coding-agent/package.json');
  const piPackage = JSON.parse(await readFile(piPackagePath, 'utf8'));
  const piIdentity = { version: piPackage.version, installedPackageJsonSha256: await sha(piPackagePath), sourcePackageJsonSha256: await sha(path.join(runtime, 'pi/package.json')), sourceLockSha256: await sha(path.join(runtime, 'pi/package-lock.json')), treeSha256: await treeHash(path.join(runtime, 'pi')) };
  if (piIdentity.version !== manifest.pi.version || piIdentity.sourcePackageJsonSha256 !== manifest.pi.sourcePackageJsonSha256 || piIdentity.sourceLockSha256 !== manifest.pi.sourceLockSha256 || piIdentity.treeSha256 !== manifest.pi.treeSha256) throw new Error('packaged private Pi identity mismatch');
  const privateVersions = {
    node: execFileSync(node, ['--version'], { encoding: 'utf8', env: { PATH: `${process.env.SystemRoot}\\System32` } }).trim(),
    vc: execFileSync(path.join(runtime, manifest.vc.path), ['--version'], { encoding: 'utf8', env: { PATH: `${process.env.SystemRoot}\\System32` } }).trim(),
    pi: piIdentity,
  };
  // The vc inside the installed bundle has to know which vc it is: `vc dev` is
  // the regression this check exists on Windows to catch, on the platform where
  // it shipped.
  assertStampedVc(privateVersions.vc, manifest);
  const installer = path.join(release, 'Void-Code-windows-x64.exe');
  const nativePty = path.join(ptyRoot, 'prebuilds/win32-x64/pty.node');
  const result = {
    package: { electron: '39.2.6', xterm: '6.0.0', nodePty: '1.1.0' }, privateVersions,
    hashes: { installer: await sha(installer), appAsar: await sha(path.join(resources, 'app.asar')), nativePty: await sha(nativePty), vc: manifest.vc.sha256, node: manifest.node.sha256, piTree: manifest.pi.treeSha256 },
    boundaries: { platform: manifest.platform, privateRuntimeOutsideAsar: true, nativeModuleOutsideAsar: true, restrictedPath: `${process.env.SystemRoot}\\System32`, electronRunAsNode: false, externalTerminalOrWsl: false },
    fixtures: runs, resizeMismatchSensitivity: sensitivityPassed,
  };
  await sleep(500);
  const finalInventory = inventory();
  if (finalInventory) throw new Error(`packaged process inventory after check: ${finalInventory}`);
  console.log(JSON.stringify(result));
} catch (error) {
  primaryError = error;
} finally {
  await cleanupStep('terminalKills', async () => {
    const failures = [];
    for (const terminal of activeTerminals) { try { terminal.kill(); } catch (error) { failures.push(error); } }
    if (failures.length) throw new AggregateError(failures, 'one or more terminal kills failed');
  });
  await cleanupStep('trackedPidWait', async () => { if (ownedPids.size) await waitGone([...ownedPids]); });
  let observed = 'unavailable';
  await cleanupStep('inventory', async () => { if (!inventory) throw new Error('inventory unavailable before preflight completed'); observed = inventory() || '[]'; });
  await cleanupStep('forcedCleanup', async () => {
    if (observed !== '[]' && observed !== 'unavailable') {
      execFileSync(powershell, ['-NoProfile', '-NonInteractive', '-Command', `${processQuery} | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }`]);
      await sleep(500);
    }
  });
  let finalInventory = 'unavailable';
  await cleanupStep('finalInventory', async () => { if (!inventory) throw new Error('inventory unavailable before preflight completed'); finalInventory = inventory() || '[]'; if (finalInventory !== '[]') throw new Error(`packaged process inventory survived forced cleanup: ${finalInventory}`); });
  await cleanupStep('tempRemoval', async () => { if (temporary) await rm(temporary, { recursive: true, force: true, maxRetries: 10, retryDelay: 200 }); });
  console.error(`VC14_CLEANUP ${JSON.stringify({ trackedPids: [...ownedPids], steps: cleanupSteps, observed, finalInventory })}`);
}
if (primaryError || cleanupErrors.length) throw new AggregateError([...(primaryError ? [primaryError] : []), ...cleanupErrors], 'Windows package check failed');
// node-pty's ConPTY worker retains native handles after both exit events; the
// process inventory above is the fixture's cleanup assertion.
process.exit(0);

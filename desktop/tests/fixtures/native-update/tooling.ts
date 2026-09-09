import { execFile, spawn } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { createReadStream } from 'node:fs';
import { cp, lstat, mkdir, readFile, readdir, readlink, realpath, rm, stat, writeFile } from 'node:fs/promises';
import process, { arch, platform } from 'node:process';
import { dirname, isAbsolute, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

const execute = promisify(execFile);
const here = dirname(fileURLToPath(import.meta.url));
const desktop = resolve(here, '../../..');
const builder = join(desktop, 'node_modules/electron-builder/cli.js');
const electronDist = join(desktop, 'node_modules/electron/dist');
const bootstrap = join(here, 'main.mjs');
const faultTemplate = join(here, 'nsis-fault.nsh');
const timeoutMs = 60_000;
const packageTimeoutMs = 5 * 60_000;
const diagnosticLimit = 64 * 1024;
const liveLaunches = new Map<string, Set<LaunchedFixture>>();

export type FixtureMode = 'normal' | 'missing' | 'wrong-token' | 'fault';
export type FixtureVersion = 'N' | 'N1';
export interface FixtureIdentity { appId: string; productName: string; executableName: string; registryGuid: string; }
export interface CapsuleMarker extends FixtureIdentity { v: 1; capsule: string; markerFile: '.native-update-capsule.json'; targetRel: 'installed' | 'installed.app'; }
export interface BootstrapConfig { transactionId: string; receiptDir: string; exitFile: string; userData: string; bootAttemptFile: string; }
export interface Receipt {
  v: 1; transactionId: string; identity: string; version: string; arch: string;
  execPath: string; resourcesPath: string; marker: string; bootstrap: 'ok'; pid: number; packaged: boolean;
}
export interface BootAttempt { v: 1; transactionId: string; pid: number; execPath: string; resourcesPath: string; packaged: boolean; mode: FixtureMode; }
export interface PredecessorReference { receiptFile: string; pid: number; }
export interface FileInventory { path: string; kind: 'file' | 'directory' | 'symlink'; mode: number; bytes?: number; sha256?: string; link?: string; }
export interface RegistryWitness { guid: string; installKey: string; uninstallKey: string; installLocationValue: 'InstallLocation'; displayVersionValue: 'DisplayVersion'; }
export interface PackagedVariant { version: FixtureVersion; mode: FixtureMode; source: string; output: string; artifact: string; marker: string; }
export interface NativeFixtureCapsule {
  root: string; marker: CapsuleMarker; markerPath: string; receiptDir: string; userData: string; target: string;
  witnessDir: string; journalDir: string; barrierDir: string; variants: Readonly<Record<string, PackagedVariant>>;
}
export interface LaunchedFixture { pid: number; exit: Promise<{ code: number | null; signal: NodeJS.Signals | null }>; diagnostics: () => string; stop: () => Promise<void>; }

const definitions = Object.freeze({ N: { version: '1.0.0', marker: 'fixture-marker-N-1.0.0' }, N1: { version: '1.0.1', marker: 'fixture-marker-N1-1.0.1' } });
const variantKeys = Object.freeze(['N', 'N1', 'N1-wrong-token', 'N1-missing', 'N1-fault'] as const);
const requireAbsolute = (value: string, label: string) => {
  if (!isAbsolute(value)) throw new Error(`${label} must be absolute`);
  return resolve(value);
};
const nsisQuoted = (value: string) => value.replace(/\$/g, () => '$$').replace(/"/g, '$\\"');

interface BoundedRunOptions { timeout?: number; env?: NodeJS.ProcessEnv; windowsVerbatimArguments?: boolean; }
async function runBounded(command: string, args: string[], cwd: string, log: string, options: BoundedRunOptions = {}): Promise<void> {
  try {
    const result = await execute(command, args, { cwd, windowsHide: true, timeout: options.timeout ?? timeoutMs, maxBuffer: 1024 * 1024, windowsVerbatimArguments: options.windowsVerbatimArguments, env: options.env ?? { ...process.env, CSC_IDENTITY_AUTO_DISCOVERY: 'false' } });
    await writeFile(log, `stdout:\n${result.stdout}\nstderr:\n${result.stderr}`, { mode: 0o600 });
  } catch (error) {
    const failure = error as { stdout?: string; stderr?: string; message: string };
    await writeFile(log, `failed: ${failure.message}\nstdout:\n${failure.stdout ?? ''}\nstderr:\n${failure.stderr ?? ''}`, { mode: 0o600 });
    throw error;
  }
}

/** Creates a fresh, private capsule. It never starts an updater or writes a success receipt. */
export async function createCapsule(root: string, run = randomUUID()): Promise<NativeFixtureCapsule> {
  const requestedCapsule = requireAbsolute(root, 'capsule');
  await mkdir(requestedCapsule, { recursive: true, mode: 0o700 });
  const capsule = await realpath(requestedCapsule);
  if ((await readdir(capsule)).length !== 0) throw new Error('refusing to use a non-empty capsule root');
  const suffix = run.replace(/[^a-z0-9]/gi, '').slice(0, 24);
  const marker: CapsuleMarker = {
    v: 1, markerFile: '.native-update-capsule.json', capsule, targetRel: platform === 'darwin' ? 'installed.app' : 'installed',
    appId: `org.voidcode.fixture.${suffix}`, productName: `Native Update Fixture ${suffix}`,
    executableName: `NativeUpdateFixture${suffix}`, registryGuid: `{${randomUUID().toUpperCase()}}`,
  };
  const markerPath = join(capsule, marker.markerFile);
  await writeFile(markerPath, JSON.stringify(marker), { flag: 'wx', mode: 0o600 });
  for (const directory of ['receipts', 'userdata', 'witnesses', 'journals', 'barriers', 'packages']) await mkdir(join(capsule, directory), { mode: 0o700 });
  const variants = Object.fromEntries(variantKeys.map((key) => [key, variantSkeleton(capsule, key)])) as Record<string, PackagedVariant>;
  return { root: capsule, marker, markerPath, receiptDir: join(capsule, 'receipts'), userData: join(capsule, 'userdata'), target: join(capsule, marker.targetRel), witnessDir: join(capsule, 'witnesses'), journalDir: join(capsule, 'journals'), barrierDir: join(capsule, 'barriers'), variants };
}

function variantSkeleton(capsule: string, key: typeof variantKeys[number]): PackagedVariant {
  const n1 = key !== 'N';
  const mode: FixtureMode = key === 'N1-wrong-token' ? 'wrong-token' : key === 'N1-missing' ? 'missing' : key === 'N1-fault' ? 'fault' : 'normal';
  const definition = definitions[n1 ? 'N1' : 'N'];
  const source = join(capsule, 'packages', key, 'source');
  return { version: n1 ? 'N1' : 'N', mode, source, output: join(capsule, 'packages', key, 'output'), artifact: '', marker: definition.marker };
}

/** Packages a tiny app with pinned electron-builder directly; product npm scripts are never used. */
export async function packageVariant(capsule: NativeFixtureCapsule, key: typeof variantKeys[number], target: 'mac' | 'win'): Promise<PackagedVariant> {
  await verifyCapsule(capsule);
  const variant = capsule.variants[key];
  if (!variant || (variant.mode === 'fault' && target !== 'win')) throw new Error(`unsupported fixture variant ${key}/${target}`);
  if (target === 'mac' && (platform !== 'darwin' || (arch !== 'arm64' && arch !== 'x64'))) throw new Error('mac fixture packaging requires an arm64 or x64 macOS host');
  await rm(variant.source, { recursive: true, force: true });
  await mkdir(join(variant.source, 'resources'), { recursive: true });
  await cp(bootstrap, join(variant.source, 'main.mjs'));
  await writeFile(join(variant.source, 'resources/full-resource-marker.txt'), variant.marker);
  await writeFile(join(variant.source, 'resources/identity.json'), JSON.stringify(capsule.marker));
  await writeFile(join(variant.source, 'resources/fixture-mode.json'), JSON.stringify({ mode: variant.mode }));
  if (variant.mode === 'fault') {
    const template = await readFile(faultTemplate, 'utf8');
    await writeFile(join(variant.source, 'fault-install.nsh'), template.replaceAll('__WITNESS_DIR__', nsisQuoted(capsule.witnessDir)));
  }
  await writeFile(join(variant.source, 'package.json'), JSON.stringify({ name: `native-update-${key.toLowerCase()}`, version: definitions[variant.version].version, private: true, type: 'module', main: 'main.mjs', build: buildConfig(capsule, variant, target) }, null, 2));
  const macArch = arch === 'arm64' ? 'arm64' : 'x64';
  const args = [builder, '--projectDir', variant.source, target === 'mac' ? '--mac' : '--win', target === 'mac' ? `--${macArch}` : '--x64', '--publish', 'never'];
  await runBounded(process.execPath, args, desktop, join(capsule.witnessDir, `package-${key}-${target}.log`), { timeout: packageTimeoutMs });
  const output = join(variant.source, 'release');
  const artifact = target === 'mac' ? join(output, macArch === 'arm64' ? 'mac-arm64' : 'mac', `${capsule.marker.executableName}.app`) : join(output, `${capsule.marker.productName} Setup ${definitions[variant.version].version}.exe`);
  if (target === 'win' && await isRegularFile(artifact)) return { ...variant, output, artifact };
  if (target === 'mac' && await isDirectory(artifact)) return { ...variant, output, artifact };
  throw new Error(`expected packaged artifact was not produced: ${artifact}`);
}

function buildConfig(capsule: NativeFixtureCapsule, variant: PackagedVariant, target: 'mac' | 'win') {
  const hostMatches = target === 'mac' ? platform === 'darwin' : platform === 'win32' && arch === 'x64';
  const common = { appId: capsule.marker.appId, productName: capsule.marker.productName, executableName: capsule.marker.executableName, electronVersion: '41.10.3', asar: true, npmRebuild: false, files: ['main.mjs', 'package.json'], extraResources: [{ from: 'resources/full-resource-marker.txt', to: 'full-resource-marker.txt' }, { from: 'resources/identity.json', to: 'identity.json' }, { from: 'resources/fixture-mode.json', to: 'fixture-mode.json' }], directories: { output: 'release' }, ...(hostMatches ? { electronDist } : {}) };
  if (target === 'mac') return { ...common, mac: { target: 'dir', identity: null, extendInfo: { LSUIElement: true } } };
  return { ...common, win: { target: 'nsis', signAndEditExecutable: false }, nsis: { oneClick: false, perMachine: false, allowElevation: false, packElevateHelper: false, allowToChangeInstallationDirectory: true, createDesktopShortcut: false, createStartMenuShortcut: false, runAfterFinish: false, deleteAppDataOnUninstall: false, guid: capsule.marker.registryGuid, artifactName: `${capsule.marker.productName} Setup ${definitions[variant.version].version}.${'${ext}'}`, ...(variant.mode === 'fault' ? { include: join(variant.source, 'fault-install.nsh') } : {}) } };
}

export async function verifyCapsule(capsule: NativeFixtureCapsule): Promise<CapsuleMarker> {
  const actual = JSON.parse(await readFile(capsule.markerPath, 'utf8')) as CapsuleMarker;
  const canonical = await realpath(capsule.root);
  const expectedTargetRel = platform === 'darwin' ? 'installed.app' : 'installed';
  if (actual.v !== 1 || actual.markerFile !== '.native-update-capsule.json' || actual.capsule !== canonical || actual.targetRel !== expectedTargetRel || actual.appId !== capsule.marker.appId || actual.productName !== capsule.marker.productName || actual.executableName !== capsule.marker.executableName || actual.registryGuid !== capsule.marker.registryGuid || !actual.appId.startsWith('org.voidcode.fixture.') || capsule.target !== join(canonical, actual.targetRel) || capsule.userData !== join(canonical, 'userdata')) throw new Error('invalid fixture capsule marker');
  return actual;
}

function capsulePath(capsule: NativeFixtureCapsule, value: string, label: string): string {
  const path = requireAbsolute(value, label);
  if (relative(capsule.root, path).startsWith('..') || path === capsule.root) throw new Error(`${label} escapes capsule`);
  return path;
}
function scrubbedEnvironment(home: string): NodeJS.ProcessEnv {
  const env: NodeJS.ProcessEnv = {};
  for (const [key, value] of Object.entries(process.env)) if (!/^VC_|API.*KEY|ELECTRON_RUN_AS_NODE$/i.test(key)) env[key] = value;
  return { ...env, HOME: home, USERPROFILE: home, APPDATA: join(home, 'AppData', 'Roaming'), LOCALAPPDATA: join(home, 'AppData', 'Local'), TEMP: join(home, 'Temp'), TMP: join(home, 'Temp'), TMPDIR: join(home, 'Temp') };
}

/** Launches only a packaged fixture in a private environment and records its actual exit. */
export async function launchFixture(capsule: NativeFixtureCapsule, executable: string, config: BootstrapConfig): Promise<LaunchedFixture> {
  await verifyCapsule(capsule);
  const executablePath = requireAbsolute(executable, 'fixture executable');
  const installedExecutable = platform === 'darwin' ? join(capsule.target, 'Contents', 'MacOS', capsule.marker.executableName) : join(capsule.target, `${capsule.marker.executableName}.exe`);
  const packagedExecutables = variantKeys.map((key) => {
    const source = capsule.variants[key].source;
    return platform === 'darwin'
      ? join(source, 'release', arch === 'arm64' ? 'mac-arm64' : 'mac', `${capsule.marker.executableName}.app`, 'Contents', 'MacOS', capsule.marker.executableName)
      : join(source, 'release', 'win-unpacked', `${capsule.marker.executableName}.exe`);
  });
  if (![installedExecutable, ...packagedExecutables].some((known) => executablePath === resolve(known))) throw new Error('fixture executable is not the exact installed target or a known packaged output');
  const receiptDir = capsulePath(capsule, config.receiptDir, 'receipt directory');
  const userData = capsulePath(capsule, config.userData, 'user data');
  const exitFile = capsulePath(capsule, config.exitFile, 'exit file');
  const bootAttemptFile = capsulePath(capsule, config.bootAttemptFile, 'boot attempt file');
  if (!/^[A-Za-z0-9-]+$/.test(config.transactionId)) throw new Error('invalid bootstrap transaction id');
  if (receiptDir !== capsule.receiptDir || userData !== capsule.userData) throw new Error('bootstrap must use capsule receipt and userdata directories');
  if (await pathKind(exitFile) !== 'missing' || await pathKind(bootAttemptFile) !== 'missing') throw new Error('bootstrap witness path already exists');
  const home = join(capsule.root, 'sandbox', config.transactionId);
  await mkdir(join(home, 'AppData', 'Roaming'), { recursive: true, mode: 0o700 }); await mkdir(join(home, 'AppData', 'Local'), { recursive: true, mode: 0o700 }); await mkdir(join(home, 'Temp'), { recursive: true, mode: 0o700 });
  await mkdir(receiptDir, { recursive: true }); await mkdir(userData, { recursive: true });
  const configFile = join(receiptDir, `${config.transactionId}.bootstrap.json`);
  await writeFile(configFile, JSON.stringify({ ...config, receiptDir, userData, exitFile, bootAttemptFile }), { flag: 'wx', mode: 0o600 });
  const output: Buffer[] = [];
  let outputBytes = 0;
  const appendDiagnostic = (chunk: Buffer) => { const available = diagnosticLimit - outputBytes; if (available <= 0) return; const retained = chunk.subarray(0, available); output.push(retained); outputBytes += retained.length; };
  const child = spawn(executablePath, [`--fixture-bootstrap=${configFile}`], { env: scrubbedEnvironment(home), stdio: ['ignore', 'pipe', 'pipe'], windowsHide: true });
  child.stdout.on('data', appendDiagnostic); child.stderr.on('data', appendDiagnostic);
  const exit = new Promise<{ code: number | null; signal: NodeJS.Signals | null }>((resolveExit, rejectExit) => {
    child.once('error', rejectExit); child.once('exit', (code, signal) => resolveExit({ code, signal }));
  });
  // Observe rejection here as well as returning it, so a spawn error cannot become a dangling rejection.
  void exit.then(() => undefined, () => undefined);
  if (!child.pid) throw new Error('fixture app did not start');
  const waitForExit = async (limit: number) => new Promise<{ code: number | null; signal: NodeJS.Signals | null } | null>((resolveWait) => {
    const timer = setTimeout(() => resolveWait(null), limit);
    void exit.then((result) => { clearTimeout(timer); resolveWait(result); }, () => { clearTimeout(timer); resolveWait(null); });
  });
  let stopPromise: Promise<void> | undefined;
  const launched: LaunchedFixture = { pid: child.pid, exit, diagnostics: () => Buffer.concat(output).toString('utf8'), stop: () => {
    if (stopPromise) return stopPromise;
    stopPromise = (async () => {
      const alreadyExited = await waitForExit(0);
      if (alreadyExited) return;
      try { await writeFile(exitFile, 'exit', { flag: 'wx', mode: 0o600 }); } catch (error) { if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error; }
      if (await waitForExit(timeoutMs)) return;
      child.kill('SIGKILL');
      const forced = await waitForExit(timeoutMs);
      if (!forced) throw new Error(`fixture ${child.pid} did not exit after exact-child termination: ${Buffer.concat(output).toString('utf8')}`);
      throw new Error(`fixture ${child.pid} ignored exit request (${forced.code ?? forced.signal}): ${Buffer.concat(output).toString('utf8')}`);
    })();
    return stopPromise;
  } };
  let launches = liveLaunches.get(capsule.root); if (!launches) { launches = new Set(); liveLaunches.set(capsule.root, launches); }
  launches.add(launched); void exit.then(() => launches?.delete(launched), () => launches?.delete(launched));
  return launched;
}

/** Windows setup uses exact direct NSIS argv: /D= is final and unquoted, with no shell. */
export async function installInitialNsis(capsule: NativeFixtureCapsule, installer: string, target: string): Promise<void> {
  await verifyCapsule(capsule); requireAbsolute(installer, 'installer');
  if (requireAbsolute(target, 'target') !== capsule.target) throw new Error('installer target is not this capsule install target');
  const home = join(capsule.root, 'sandbox', 'initial-nsis');
  await mkdir(join(home, 'AppData', 'Roaming'), { recursive: true, mode: 0o700 }); await mkdir(join(home, 'AppData', 'Local'), { recursive: true, mode: 0o700 }); await mkdir(join(home, 'Temp'), { recursive: true, mode: 0o700 });
  // NSIS parses its final /D= argument itself; verbatim argv is required only for this direct call.
  await runBounded(installer, ['/S', '/currentuser', `/D=${target}`], capsule.root, join(capsule.witnessDir, 'initial-nsis.log'), { env: scrubbedEnvironment(home), windowsVerbatimArguments: true });
}
export async function copyInitialMac(capsule: NativeFixtureCapsule, bundle: string, target: string): Promise<void> {
  await verifyCapsule(capsule); requireAbsolute(bundle, 'bundle');
  if (requireAbsolute(target, 'target') !== capsule.target) throw new Error('bundle target is not this capsule install target');
  await cp(bundle, target, { recursive: true, preserveTimestamps: true, verbatimSymlinks: true });
}
export async function readAndValidateReceipt(file: string, expected: Omit<Receipt, 'pid'>): Promise<Receipt> {
  const receipt = JSON.parse(await readFile(requireAbsolute(file, 'receipt'), 'utf8')) as Receipt;
  const equal = (key: keyof Omit<Receipt, 'pid'>) => receipt[key] === expected[key];
  if (receipt.v !== 1 || receipt.bootstrap !== 'ok' || !Number.isSafeInteger(receipt.pid) || receipt.pid < 1 || !equal('transactionId') || !equal('identity') || !equal('version') || !equal('arch') || !equal('marker') || !equal('packaged') || await realpath(receipt.execPath) !== await realpath(expected.execPath) || await realpath(receipt.resourcesPath) !== await realpath(expected.resourcesPath)) throw new Error('invalid packaged bootstrap receipt');
  return receipt;
}
export async function readAndValidateBootAttempt(file: string, expected: Omit<BootAttempt, 'pid'>): Promise<BootAttempt> {
  const attempt = JSON.parse(await readFile(requireAbsolute(file, 'boot attempt'), 'utf8')) as BootAttempt;
  if (attempt.v !== 1 || !Number.isSafeInteger(attempt.pid) || attempt.pid < 1 || attempt.transactionId !== expected.transactionId || attempt.execPath !== expected.execPath || attempt.resourcesPath !== expected.resourcesPath || attempt.packaged !== expected.packaged || attempt.mode !== expected.mode) throw new Error('invalid packaged boot attempt');
  return attempt;
}
/** Later update requests must bind to a receipt written by the actual predecessor process. */
export async function requirePredecessorReference(reference: PredecessorReference): Promise<Receipt> {
  const receipt = JSON.parse(await readFile(requireAbsolute(reference.receiptFile, 'predecessor receipt'), 'utf8')) as Receipt;
  if (receipt.v !== 1 || receipt.bootstrap !== 'ok' || !receipt.packaged || !Number.isSafeInteger(reference.pid) || reference.pid < 1 || receipt.pid !== reference.pid) throw new Error('invalid predecessor receipt/PID reference');
  return receipt;
}

async function sha256(file: string): Promise<string> { return new Promise((resolveHash, rejectHash) => { const hash = createHash('sha256'); const stream = createReadStream(file); stream.on('error', rejectHash); stream.on('data', (chunk) => hash.update(chunk)); stream.on('end', () => resolveHash(hash.digest('hex'))); }); }
/** A symlink-preserving, mode-preserving, byte-content inventory for immutable predecessor checks. */
export async function inventory(root: string): Promise<FileInventory[]> {
  const canonicalRoot = await realpath(requireAbsolute(root, 'inventory root'));
  const entries: FileInventory[] = [];
  async function visit(current: string): Promise<void> {
    const info = await lstat(current); const path = current.slice(canonicalRoot.length) || '.';
    if (info.isSymbolicLink()) { entries.push({ path, kind: 'symlink', mode: info.mode & 0o777, link: await readlink(current) }); return; }
    if (info.isDirectory()) { entries.push({ path, kind: 'directory', mode: info.mode & 0o777 }); for (const child of await readdir(current)) await visit(join(current, child)); return; }
    if (!info.isFile()) throw new Error(`unsupported inventory entry: ${current}`);
    entries.push({ path, kind: 'file', mode: info.mode & 0o777, bytes: info.size, sha256: await sha256(current) });
  }
  await visit(canonicalRoot); return entries.sort((a, b) => a.path.localeCompare(b.path));
}
export function assertSameInventory(expected: readonly FileInventory[], actual: readonly FileInventory[]): void { if (JSON.stringify(expected) !== JSON.stringify(actual)) throw new Error('installed bundle inventory differs from immutable predecessor'); }

/** Registry paths that later witnesses must query with reg.exe; this performs no registry validation itself. */
export function registryWitness(guid: string): RegistryWitness { return { guid, installKey: `HKCU\\Software\\${guid}`, uninstallKey: `HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall\\${guid.replace(/\\/g, ' - ')}`, installLocationValue: 'InstallLocation', displayVersionValue: 'DisplayVersion' }; }
/** Removes only this fixture's NSIS target and its two GUID-derived HKCU keys. */
export async function cleanupWindowsFixture(capsule: NativeFixtureCapsule): Promise<void> {
  const marker = await verifyCapsule(capsule);
  if (platform !== 'win32') return;
  const target = capsule.target;
  const uninstaller = join(target, `Uninstall ${marker.productName}.exe`);
  const cleanupHome = join(capsule.root, 'sandbox', 'cleanup-nsis');
  await mkdir(join(cleanupHome, 'Temp'), { recursive: true, mode: 0o700 });
  if (await isRegularFile(uninstaller)) {
    // `_?=` is deliberately final and unquoted, just like the installation /D= argument.
    try { await runBounded(uninstaller, ['/S', `/_?=${target}`], capsule.root, join(capsule.witnessDir, 'cleanup-nsis.log'), { env: scrubbedEnvironment(cleanupHome), windowsVerbatimArguments: true }); } catch { /* exact own-key removal below is the constrained fallback */ }
  }
  const witness = registryWitness(marker.registryGuid);
  for (const key of [witness.installKey, witness.uninstallKey]) {
    try { await runBounded('reg.exe', ['delete', key, '/f'], capsule.root, join(capsule.witnessDir, `cleanup-${createHash('sha256').update(key).digest('hex')}.log`)); } catch { /* absent own key is an acceptable cleanup result */ }
  }
  await rm(target, { recursive: true, force: true });
}
export async function cleanupCapsule(capsule: NativeFixtureCapsule): Promise<void> {
  await verifyCapsule(capsule);
  const launches = liveLaunches.get(capsule.root);
  if (launches?.size) await Promise.all([...launches].map((launch) => launch.stop()));
  await cleanupWindowsFixture(capsule);
  if (liveLaunches.get(capsule.root)?.size) throw new Error('refusing to remove capsule with live fixture children');
  await rm(capsule.root, { recursive: true, force: false });
}
async function pathKind(path: string): Promise<'file' | 'directory' | 'missing' | 'other'> {
  try {
    const info = await stat(requireAbsolute(path, 'path'));
    return info.isFile() ? 'file' : info.isDirectory() ? 'directory' : 'other';
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') return 'missing';
    throw error;
  }
}
export async function isRegularFile(path: string): Promise<boolean> { return (await pathKind(path)) === 'file'; }
export async function isDirectory(path: string): Promise<boolean> { return (await pathKind(path)) === 'directory'; }

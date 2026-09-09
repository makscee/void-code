import { spawn } from 'node:child_process';
import type { ChildProcess } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { constants, existsSync } from 'node:fs';
import { cp, lstat, mkdir, mkdtemp, open, readFile, readdir, realpath, rename, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { arch, platform } from 'node:process';
import { afterAll, describe, expect, it, vi } from 'vitest';
import { archiveSeed, finishOperation } from './fixtures/native-update/operations.ts';
import type { SeedArchiveOptions } from './fixtures/native-update/operations.ts';
import {
  assertSameInventory, copyInitialMac, createCapsule, installInitialNsis, inventory,
  launchFixture, packageVariant, readAndValidateBootAttempt, readAndValidateReceipt, registryWitness, verifyCapsule,
} from './fixtures/native-update/tooling.ts';
import type {
  BootAttempt, FileInventory, FixtureMode, LaunchedFixture, NativeFixtureCapsule, PackagedVariant, Receipt,
} from './fixtures/native-update/tooling.ts';

const root = resolve(import.meta.dirname, '../..');
const entrypoint = join(root, 'desktop/experiments/update-native/main.go');
const enabled = process.env.VOID_NATIVE_UPDATE_PROBE === '1';
const host = platform === 'darwin' && ['arm64', 'x64'].includes(arch) ? 'mac'
  : platform === 'win32' && arch === 'x64' ? 'win' : undefined;
const deadlineMs = 45_000;
const barriers: readonly string[] = host === 'mac'
  ? ['before-mutation', 'after-old-to-backup', 'after-new-to-target-before-commit']
  : ['before-mutation'];
const pause = (ms: number) => new Promise<void>((done) => setTimeout(done, ms));
const hash = async (file: string) => createHash('sha256').update(await readFile(file)).digest('hex');

interface Request {
  v: 1; op: 'install' | 'recover'; transactionId: string; capsule: string; identity: string;
  from: { version: '1.0.0'; target: string; package: string; predecessorReceipt: string; predecessorPid: number };
  to?: { version: '1.0.1'; package: string; marker: string };
  receiptDir: string; barrierDir: string; deadlineMs: number;
}
interface Event { v: number; event: string; transactionId?: string; name?: string; }
interface Running {
  child: ChildProcess;
  closed: Promise<{ code: number | null; signal: NodeJS.Signals | null }>;
  done: Promise<{ code: number | null; events: Event[] }>;
}
interface Suite {
  c: NativeFixtureCapsule; binary: string; n: PackagedVariant; n1: PackagedVariant;
  wrong: PackagedVariant; missing: PackagedVariant; fault?: PackagedVariant;
  immutable: FileInventory[]; nHash: string; sentinel: Running; sentinels: Map<string, string>;
}
interface Context {
  s: Suite; id: string; pre: Receipt; preFile: string; before: FileInventory[];
  manual: LaunchedFixture[]; transactions: Set<string>; helpers: Running[];
  boots: Awaited<ReturnType<typeof bootInventory>>;
}
let suite: Promise<Suite> | undefined;
let capsule: NativeFixtureCapsule | undefined;
let poisoned = false;
const children: Running[] = [];
const manualChildren: LaunchedFixture[] = [];
const contexts: Context[] = [];
const readerEvidence: NativeFixtureCapsule[] = [];

// Direct regular files only: at most 64 files / 4 MiB per directory, opaque bytes.
async function retainEvidence(directory: string, output: string, allowed: RegExp) {
  const directoryStat = await lstat(directory);
  if (!directoryStat.isDirectory() || directoryStat.isSymbolicLink()) throw new Error('evidence directory redirected');
  await mkdir(output, { recursive: true });
  let count = 0;
  for (const name of (await readdir(directory)).sort()) {
    if (!allowed.test(name)) continue;
    const file = join(directory, name);
    const before = await lstat(file);
    if (!before.isFile() || before.isSymbolicLink() || before.size > 64 * 1024) continue;
    if (count >= 64) break;
    const handle = await open(file, constants.O_RDONLY | (constants.O_NOFOLLOW ?? 0));
    try {
      const current = await handle.stat();
      if (!current.isFile() || current.dev !== before.dev || current.ino !== before.ino || current.size > 64 * 1024) continue;
      const bytes = Buffer.alloc(64 * 1024 + 1);
      let length = 0;
      while (length < bytes.length) {
        const result = await handle.read(bytes, length, bytes.length - length, length);
        if (!result.bytesRead) break;
        length += result.bytesRead;
      }
      if (length > 64 * 1024) continue;
      await writeFile(join(output, name), bytes.subarray(0, length), { flag: 'wx' });
      count++;
    } finally { await handle.close(); }
  }
}

async function present(file: string): Promise<boolean> {
  try { await lstat(file); return true; }
  catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ENOENT') return false;
    throw error;
  }
}
function alive(pid: number): boolean {
  expect(Number.isSafeInteger(pid) && pid > 0, 'only a witnessed positive PID may be inspected').toBe(true);
  try { process.kill(pid, 0); return true; }
  catch (error) {
    if ((error as NodeJS.ErrnoException).code === 'ESRCH') return false;
    throw error;
  }
}
async function until(predicate: () => Promise<boolean> | boolean, label: string, ms = deadlineMs) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (await predicate()) return;
    await pause(40);
  }
  throw new Error(`native watchdog: ${label}; preserve capsule ${capsule?.root ?? '(setup)'}`);
}
async function bounded<T>(promise: Promise<T>, label: string, ms = deadlineMs): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([promise, new Promise<never>((_, reject) => {
      timer = setTimeout(() => reject(new Error(`native watchdog: ${label}`)), ms);
    })]);
  } finally { clearTimeout(timer); }
}

// All test-owned helper/tool children are tracked immediately. Parsing happens in
// a promise continuation AFTER close, never inside an EventEmitter callback.
function run(command: string, args: string[], cwd: string, log: string, input?: Request, ms = deadlineMs, logWrite: typeof writeFile = writeFile): Running {
  const child = spawn(command, args, { cwd, windowsHide: true, stdio: ['pipe', 'pipe', 'pipe'] });
  const stdout: Buffer[] = [];
  const stderr: Buffer[] = [];
  let size = 0;
  let overflow = false;
  const capture = (chunks: Buffer[]) => (chunk: Buffer) => {
    size += chunk.length;
    if (size <= 4 * 1024 * 1024) chunks.push(chunk);
    else { overflow = true; child.kill('SIGKILL'); }
  };
  child.stdout.on('data', capture(stdout));
  child.stderr.on('data', capture(stderr));
  const closed = new Promise<{ code: number | null; signal: NodeJS.Signals | null }>((done, reject) => {
    child.once('error', reject);
    child.once('close', (code, signal) => done({ code, signal }));
  });
  let stdinError: Error | undefined;
  child.stdin.on('error', (error: Error) => { stdinError = error; });
  child.stdin.end(input ? `${JSON.stringify(input)}\n` : undefined);
  let expired = false;
  const timer = setTimeout(() => { expired = true; child.kill('SIGKILL'); }, ms);
  const done = bounded(closed.then(async (result) => {
    const text = Buffer.concat(stdout).toString('utf8');
    await logWrite(log, `stdout:\n${text}\nstderr:\n${Buffer.concat(stderr).toString('utf8')}`);
    if (expired || overflow) throw new Error(`child watchdog/output bound: ${command}; ${log}`);
    if (stdinError) throw stdinError;
    const events = input && text.trim() ? text.trim().split(/\r?\n/).map((line) => JSON.parse(line) as Event) : [];
    if (input) for (const event of events) {
      expect(event.v).toBe(1);
      expect(['barrier', 'started', 'refused', 'failed', 'exited']).toContain(event.event);
    }
    return { code: result.code, events };
  }), `reap ${command}`, ms + 5_000).finally(() => { clearTimeout(timer); });
  // Early spawn/watchdog errors remain rejected for the caller without becoming unhandled.
  void done.then(() => undefined, () => undefined);
  void closed.then(() => undefined, () => undefined);
  const running = { child, closed, done };
  children.push(running);
  return running;
}
async function finish(running: Running) {
  return finishOperation(running.done, { time: { bounded }, reap: () => killAndReap(running) });
}
async function killAndReap(running: Running) {
  if (running.child.exitCode === null && running.child.signalCode === null) running.child.kill('SIGKILL');
  await bounded(running.closed, 'exact ChildProcess reap', 5_000);
}
async function tool(s: Suite, command: string, args: string[]) {
  const log = join(s.c.witnessDir, `tool-${randomUUID()}.log`);
  const result = await run(command, args, s.c.root, log).done;
  expect(result.code, log).toBe(0);
  return readFile(log, 'utf8');
}
function paths(c: NativeFixtureCapsule) {
  return host === 'mac'
    ? { execPath: join(c.target, 'Contents/MacOS', c.marker.executableName), resourcesPath: join(c.target, 'Contents/Resources') }
    : { execPath: join(c.target, `${c.marker.executableName}.exe`), resourcesPath: join(c.target, 'resources') };
}
const receiptPath = (c: NativeFixtureCapsule, id: string) => join(c.receiptDir, `${id}.json`);
const bootPath = (c: NativeFixtureCapsule, id: string) => join(c.receiptDir, `${id}.boot.json`);
const barrierPath = (x: Context, id: string, name: string) => join(x.s.c.barrierDir, `${id}.${name}.json`);

async function packageWave<T>(work: Promise<T>[]): Promise<T[]> {
  const results = await Promise.allSettled(work);
  const failures = results.filter((result) => result.status === 'rejected');
  if (failures.length) throw new AggregateError(failures.map((result) => result.reason), 'fixture packaging failed');
  return results.map((result) => {
    if (result.status !== 'fulfilled') throw new Error('unreachable rejected packaging result');
    return result.value;
  });
}
async function setup(): Promise<Suite> {
  const c = await createCapsule(await mkdtemp(join(tmpdir(), 'native update テスト-')));
  capsule = c;
  // Retain packages and all diagnostics, including on a failed/uncertain teardown.
  console.info(`native S0 retained capsule: ${c.root}`);
  const binary = join(c.root, `native-update-probe${host === 'win' ? '.exe' : ''}`);
  const build = await run('go', ['build', '-o', binary, './desktop/experiments/update-native'], root,
    join(c.witnessDir, 'go-build.log'), undefined, 120_000).done;
  expect(build.code, 'Go build failed; this is not the missing-entrypoint RED').toBe(0);
  // Two bounded waves avoid concurrent output collisions and cap package setup
  // at roughly ten minutes (each immutable variant has a distinct projectDir).
  const target = host === 'mac' ? 'mac' : 'win';
  const normal = await packageWave([packageVariant(c, 'N', target), packageVariant(c, 'N1', target)]);
  const n = normal[0]!;
  const n1 = normal[1]!;
  const variants = await packageWave([
    packageVariant(c, 'N1-wrong-token', target), packageVariant(c, 'N1-missing', target),
    ...(host === 'win' ? [packageVariant(c, 'N1-fault', target)] : []),
  ]);
  const wrong = variants[0]!;
  const missing = variants[1]!;
  const fault = variants[2];
  const sentinels = new Map<string, string>();
  for (const name of ['session', 'token', 'settings']) {
    const file = join(c.userData, `${name}.sentinel.json`);
    await writeFile(file, JSON.stringify({ name, value: randomUUID() }), { flag: 'wx' });
    sentinels.set(file, await hash(file));
  }
  const sentinel = run(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], c.root,
    join(c.witnessDir, 'unrelated-process.log'), undefined, 60 * 60_000);
  expect(sentinel.child.pid).toBeGreaterThan(0);
  return { c, binary, n, n1, wrong, missing, fault, sentinels, sentinel,
    immutable: host === 'mac' ? await inventory(n.artifact) : [],
    nHash: host === 'win' ? await hash(n.artifact) : '' };
}
async function ready() {
  expect(existsSync(entrypoint), `missing helper entrypoint: ${entrypoint}`).toBe(true);
  expect(poisoned, 'earlier ownership/oracle failure: refusing to overwrite retained evidence').toBe(false);
  suite ??= setup();
  return suite;
}
async function validateReceipt(c: NativeFixtureCapsule, id: string, version: '1.0.0' | '1.0.1', marker: string) {
  const file = receiptPath(c, id);
  await until(() => present(file), `real packaged receipt ${id}`);
  return readAndValidateReceipt(file, { v: 1, transactionId: id, identity: c.marker.appId,
    version, arch, ...paths(c), marker, bootstrap: 'ok', packaged: true });
}
async function manualN(x: Context, label: string, leaveAlive = false) {
  const c = x.s.c;
  const id = `${x.id}-${label}-${randomUUID()}`;
  x.transactions.add(id);
  const app = await launchFixture(c, paths(c).execPath, { transactionId: id, receiptDir: c.receiptDir,
    userData: c.userData, exitFile: join(c.receiptDir, `${id}.exit`), bootAttemptFile: bootPath(c, id) });
  manualChildren.push(app);
  x.manual.push(app);
  const receipt = await validateReceipt(c, id, '1.0.0', x.s.n.marker);
  const boot = await readAndValidateBootAttempt(bootPath(c, id), {
    v: 1, transactionId: id, ...paths(c), packaged: true, mode: 'normal',
  });
  expect(receipt.pid).toBe(app.pid);
  expect(boot.pid).toBe(app.pid);
  expect(alive(app.pid)).toBe(true);
  expect(await readFile(join(paths(c).resourcesPath, 'full-resource-marker.txt'), 'utf8')).toBe(x.s.n.marker);
  if (!leaveAlive) {
    await app.stop();
    await until(() => !alive(app.pid), 'manual N PID disappears');
  }
  return { receipt, file: receiptPath(c, id) };
}
async function seed(s: Suite, live: boolean): Promise<Context> {
  // Only setup may reinstall. Recovery oracles below NEVER call seed/copy/NSIS.
  const id = `update-${randomUUID()}`;
  for (const source of [s.c.target, join(s.c.root, 'backup.app')]) {
    const destination = `${source}.archive-${id}`;
    const options = await seedOptions(s.c, source, destination);
    const trace: { attempt: number; elapsedMs: number; code?: string }[] = [];
    options.observe = (event) => { trace.push(event); };
    try {
      await archiveSeed({ source, destination }, {
        sourceKind: async (path) => await present(path) ? 'present' : 'missing', rename,
      }, options);
    } finally {
      await writeFile(join(s.c.witnessDir, `archive-${randomUUID()}.json`), JSON.stringify({ source, destination, trace }));
    }
  }
  if (host === 'mac') await copyInitialMac(s.c, s.n.artifact, s.c.target);
  else await installInitialNsis(s.c, s.n.artifact, s.c.target);
  const x: Context = { s, id, pre: {} as Receipt, preFile: '', before: [], manual: [], transactions: new Set(), helpers: [], boots: [] };
  contexts.push(x);
  const pre = await manualN(x, 'preinstall', live);
  x.boots = await bootInventory(s.c);
  x.pre = pre.receipt;
  x.preFile = pre.file;
  x.before = await inventory(s.c.target);
  await writeFile(join(s.c.witnessDir, `${id}-preinstall-inventory.json`), JSON.stringify(x.before));
  if (host === 'mac') assertSameInventory(s.immutable, x.before);
  else await registry(x, '1.0.0');
  return x;
}
function request(x: Context, variant = x.s.n1, id = x.id): Request {
  const c = x.s.c;
  return { v: 1, op: 'install', transactionId: id, capsule: c.root, identity: c.marker.appId,
    from: { version: '1.0.0', target: c.target, package: x.s.n.artifact, predecessorReceipt: x.preFile, predecessorPid: x.pre.pid },
    to: { version: '1.0.1', package: variant.artifact, marker: variant.marker },
    receiptDir: c.receiptDir, barrierDir: c.barrierDir, deadlineMs };
}
function invoke(x: Context, value: Request) {
  x.transactions.add(value.transactionId);
  const h = run(x.s.binary, [], x.s.c.root,
    join(x.s.c.witnessDir, `${value.transactionId}-${value.op}-${randomUUID()}.log`), value, 4 * deadlineMs);
  x.helpers.push(h);
  return h;
}
async function observeBarrier(x: Context, h: Running, id: string, name: string, variant = x.s.n1) {
  expect(await bootInventory(x.s.c)).toEqual(x.boots);
  const file = barrierPath(x, id, name);
  await until(async () => {
    if (await present(file)) return true;
    if (h.child.exitCode !== null || h.child.signalCode !== null) {
      await finish(h);
      throw new Error(`helper exited before durable ${name}`);
    }
    return false;
  }, `barrier ${name}`);
  expect(JSON.parse(await readFile(file, 'utf8'))).toEqual({ v: 1, event: 'barrier', transactionId: id, name });
  expect(h.child.pid).toBeGreaterThan(0);
  expect(alive(h.child.pid!)).toBe(true);
  expect(await present(receiptPath(x.s.c, id))).toBe(false);
  expect(await present(bootPath(x.s.c, id))).toBe(false);
  if (name === 'before-mutation') assertSameInventory(x.before, await inventory(x.s.c.target));
  if (host === 'mac' && name !== 'before-mutation') {
    const backup = await inventory(join(x.s.c.root, 'backup.app'));
    assertSameInventory(x.s.immutable, backup);
    expect(backup.filter((e) => e.kind === 'symlink' && e.path.includes('Frameworks')).length).toBeGreaterThan(0);
    expect(backup.filter((e) => e.kind === 'file' && (e.mode & 0o111) !== 0).length).toBeGreaterThan(0);
    if (name === 'after-old-to-backup') expect(await present(x.s.c.target)).toBe(false);
    else assertSameInventory(await inventory(variant.artifact), await inventory(x.s.c.target));
  }
  // An actual bounded hold detects a helper which writes the marker but ignores ACK.
  await pause(200);
  expect(alive(h.child.pid!)).toBe(true);
  expect(await present(bootPath(x.s.c, id))).toBe(false);
  expect(await bootInventory(x.s.c)).toEqual(x.boots);
  const next = barriers[barriers.indexOf(name) + 1];
  if (next) expect(await present(barrierPath(x, id, next))).toBe(false);
}
async function ack(x: Context, id: string, name: string) {
  const continuation = barrierPath(x, id, name).replace(/\.json$/, '.continue');
  const temporary = `${continuation}.tmp`;
  await writeFile(temporary, 'continue', { flag: 'wx', mode: 0o600 });
  await rename(temporary, continuation);
}
async function drive(x: Context, h: Running, variant = x.s.n1, id = x.id, start = 0) {
  for (const name of barriers.slice(start)) {
    await observeBarrier(x, h, id, name, variant);
    await ack(x, id, name);
  }
}
async function attempt(x: Context, mode: FixtureMode, id = x.id): Promise<BootAttempt> {
  await until(() => present(bootPath(x.s.c, id)), `A-owned ${mode} boot attempt`);
  return readAndValidateBootAttempt(bootPath(x.s.c, id), {
    v: 1, transactionId: id, ...paths(x.s.c), packaged: true, mode,
  });
}
async function stopAttempt(x: Context, id: string) {
  const records = (await bootInventory(x.s.c)).filter((entry) => entry.file.startsWith(`${id}.boot.json.attempt-`));
  if (!records.length) return; // canonical receipt is never a PID fallback
  const errors: unknown[] = [];
  const pids: number[] = [];
  for (const { file, value: boot } of records) {
    try {
      expect(boot.v).toBe(1);
      expect(boot.transactionId).toBe(id);
      expect(boot.packaged).toBe(true);
      expect(boot.execPath).toBe(paths(x.s.c).execPath);
      expect(boot.resourcesPath).toBe(paths(x.s.c).resourcesPath);
      expect(['normal', 'missing', 'wrong-token', 'fault']).toContain(boot.mode);
      expect(Number.isSafeInteger(boot.pid) && boot.pid > 0).toBe(true);
      expect(file).toMatch(new RegExp(`\\.attempt-${boot.pid}-[0-9a-f-]{36}\\.json$`));
      pids.push(boot.pid);
    } catch (error) { errors.push(error); }
  }
  if (pids.length) {
    const exit = join(x.s.c.receiptDir, `${id}.exit`);
    if (!await present(exit)) await writeFile(exit, 'exit', { flag: 'wx' });
    // B-launched apps are NOT our ChildProcess: never signal a receipt PID.
    for (const pid of pids) {
      try { await until(() => !alive(pid), `fixture-owned exit ${id}/${pid}`); }
      catch (error) { errors.push(error); }
    }
  }
  if (errors.length) throw new AggregateError(errors, `fixture ledger cleanup ${id}`);
}
function checkRegistryObservation(text: string, target: string, version: string) {
  expect(Buffer.byteLength(text, 'utf8')).toBeLessThanOrEqual(64 * 1024);
  // Deliberately fixed, flat wire schema: reject duplicate keys BEFORE JSON.parse
  // can discard them. JSON.parse additionally validates string escape syntax.
  const string = '"(?:[^"\\\\\\x00-\\x1f]|\\\\.)*"';
  expect(text).toMatch(new RegExp(`^\\s*\\{\\s*"InstallLocation"\\s*:\\s*${string}\\s*,\\s*"DisplayVersion"\\s*:\\s*${string}\\s*\\}\\s*$`));
  const value = JSON.parse(text);
  expect(Object.keys(value)).toEqual(['InstallLocation', 'DisplayVersion']);
  for (const field of Object.values(value)) {
    expect(typeof field).toBe('string');
    expect(field).not.toBe('');
  }
  expect(value.InstallLocation).toBe(target);
  expect(value.DisplayVersion).toBe(version);
}
async function registry(x: Context, version: string) {
  const witness = registryWitness(x.s.c.marker.registryGuid);
  const quote = (value: string) => `'${value.replaceAll("'", "''")}'`;
  const subkey = (key: string) => {
    expect(key.startsWith('HKCU\\')).toBe(true);
    return quote(key.slice('HKCU\\'.length));
  };
  // Test-only OS read; no renderer input or installer launch. Pinned installer.nsh
  // writes location to the install key and version to the uninstall key.
  const script = `
$ProgressPreference = 'SilentlyContinue'
# Process-local probe setup: prevent first module load progress from polluting stderr.
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
function Read-StringValue([string]$path, [string]$name) {
  $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey($path, $false)
  if ($null -eq $key) { throw 'Missing registry key' }
  try {
    if ($key.GetValueKind($name) -ne [Microsoft.Win32.RegistryValueKind]::String) { throw 'Expected REG_SZ' }
    $value = $key.GetValue($name, $null, [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    if ($value -isnot [string] -or $value.Length -eq 0) { throw 'Missing/empty registry string' }
    return $value
  } finally { $key.Dispose() }
}
try {
  $observation = [ordered]@{
    InstallLocation = Read-StringValue ${subkey(witness.installKey)} ${quote(witness.installLocationValue)}
    DisplayVersion = Read-StringValue ${subkey(witness.uninstallKey)} ${quote(witness.displayVersionValue)}
  }
  $json = ConvertTo-Json -InputObject $observation -Compress
  if ([System.Text.Encoding]::UTF8.GetByteCount($json) -gt 65536) { throw 'Registry output bound' }
  [Console]::Out.WriteLine($json)
} catch { [Console]::Error.WriteLine($_.Exception.Message); exit 1 }
`;
  const output = await tool(x.s, 'powershell.exe', ['-NoProfile', '-NonInteractive', '-EncodedCommand',
    Buffer.from(script, 'utf16le').toString('base64')]);
  const envelope = /^stdout:\n([\s\S]*)\nstderr:\n$/.exec(output);
  expect(envelope, 'registry probe must emit only JSON, with empty stderr').not.toBeNull();
  checkRegistryObservation(envelope![1]!, x.s.c.target, version);
}

// External ownership seam only: no retry or rename policy lives in this adapter.
async function seedOptions(c: NativeFixtureCapsule, source: string, destination: string): Promise<SeedArchiveOptions> {
  const initial = await present(source) ? await lstat(source) : undefined;
  return {
    platform, now: () => performance.now(), sleep: pause,
    verify: async () => {
      await verifyCapsule(c);
      if (![c.target, join(c.root, 'backup.app')].includes(source)) throw new Error('unowned seed source');
      if (!initial) {
        if (await present(source)) throw new Error('seed source appeared after initial absence');
        return;
      }
      const current = await lstat(source);
      if (current.dev !== initial.dev || current.ino !== initial.ino || current.isSymbolicLink() !== initial.isSymbolicLink()) {
        throw new Error('seed source identity changed');
      }
      // Case03 intentionally leaves an owned junction. Never read through that link.
      if (!initial.isSymbolicLink()) {
        if (!current.isDirectory() || await realpath(source) !== source) throw new Error('seed source redirected');
        const resources = platform === 'darwin' ? join(source, 'Contents/Resources') : join(source, 'resources');
        const identity = JSON.parse(await readFile(join(resources, 'identity.json'), 'utf8'));
        if (JSON.stringify(identity) !== JSON.stringify(c.marker)) throw new Error('seed app identity changed');
      }
    },
    destinationExists: () => present(destination),
    sourceIsLink: async () => {
      if (!initial) return false;
      const current = await lstat(source);
      if (current.dev !== initial.dev || current.ino !== initial.ino || current.isSymbolicLink() !== initial.isSymbolicLink()) {
        throw new Error('seed source identity changed');
      }
      return current.isSymbolicLink();
    },
  };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((yes, no) => { resolve = yes; reject = no; });
  return { promise, resolve, reject };
}
function settlement<T>(promise: Promise<T>) {
  const state: { status: 'pending' | 'fulfilled' | 'rejected'; value?: T; error?: unknown } = { status: 'pending' };
  const observed = promise.then(
    (value) => { state.status = 'fulfilled'; state.value = value; },
    (error: unknown) => { state.status = 'rejected'; state.error = error; },
  );
  return { state, observed };
}
function retainedErrors(error: unknown): unknown[] {
  if (error instanceof AggregateError) return [error, ...error.errors.flatMap(retainedErrors)];
  if (error instanceof Error && error.cause !== undefined) return [error, ...retainedErrors(error.cause)];
  return [error];
}

describe.sequential('portable finish failure controls', () => {
  it.each([0, 7])('preserves natural code %i and exact result without reap', async (code) => {
    const value = { code, events: [] };
    const reap = vi.fn(async () => {});
    expect(await finishOperation(Promise.resolve(value), { time: { bounded }, reap })).toBe(value);
    expect(reap).not.toHaveBeenCalled();
  });
  it('joins a 60s settlement without adding a 45s watchdog', async () => {
    vi.useFakeTimers();
    const done = deferred<{ code: number; events: Event[] }>();
    const reap = vi.fn(async () => {});
    const result = settlement(finishOperation(done.promise, { time: { bounded }, reap }));
    try {
      await vi.advanceTimersByTimeAsync(45_001);
      expect(result.state.status).toBe('pending');
      expect(reap).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(14_999);
      const value = { code: 7, events: [] };
      done.resolve(value);
      await result.observed;
      expect(result.state).toEqual({ status: 'fulfilled', value });
      expect(result.state.value).toBe(value);
      expect(reap).not.toHaveBeenCalled();
    } finally { done.resolve({ code: 7, events: [] }); await result.observed; vi.useRealTimers(); }
  });
  it('two joins at 150s/160s preserve the original 180s owner deadline', async () => {
    vi.useFakeTimers();
    const done = deferred<never>();
    const original = new Error('original owner deadline');
    const owner = setTimeout(() => done.reject(original), 180_000);
    const ownerState = settlement(done.promise);
    const reap = vi.fn(async () => {});
    try {
      await vi.advanceTimersByTimeAsync(150_000);
      const first = settlement(finishOperation(done.promise, { time: { bounded }, reap }));
      await vi.advanceTimersByTimeAsync(10_000);
      const second = settlement(finishOperation(done.promise, { time: { bounded }, reap }));
      await vi.advanceTimersByTimeAsync(19_999);
      expect(first.state.status).toBe('pending');
      expect(second.state.status).toBe('pending');
      expect(reap).not.toHaveBeenCalled();
      await vi.advanceTimersByTimeAsync(1);
      await Promise.all([first.observed, second.observed]);
      expect(first.state.error).toBe(original);
      expect(second.state.error).toBe(original);
      expect(reap).toHaveBeenCalledTimes(2);
      expect(vi.getTimerCount()).toBe(0);
    } finally { clearTimeout(owner); done.reject(original); await ownerState.observed; vi.useRealTimers(); }
  });
  it('retains original rejection identity and reaps exactly once, after rejection', async () => {
    const original = new Error('operation failed');
    const order: string[] = [];
    const done = Promise.resolve().then(() => { order.push('reject'); throw original; });
    const reap = vi.fn(async () => { order.push('reap'); });
    await expect(finishOperation(done, { time: { bounded }, reap })).rejects.toBe(original);
    expect(reap).toHaveBeenCalledTimes(1);
    expect(order).toEqual(['reject', 'reap']);
  });
  it('retains BOTH operation and 5s reap errors by identity', async () => {
    vi.useFakeTimers();
    const original = new Error('operation failed');
    const cleanup = new Error('exact child reap failed');
    const reap = vi.fn(async () => { await pause(5_000); throw cleanup; });
    const result = settlement(finishOperation(Promise.reject(original), { time: { bounded }, reap }));
    try {
      await vi.advanceTimersByTimeAsync(4_999);
      expect(result.state.status).toBe('pending');
      expect(reap).toHaveBeenCalledTimes(1);
      await vi.advanceTimersByTimeAsync(1);
      await result.observed;
      expect(result.state.status).toBe('rejected');
      expect(retainedErrors(result.state.error)).toContain(original);
      expect(retainedErrors(result.state.error)).toContain(cleanup);
      expect(vi.getTimerCount()).toBe(0);
    } finally { await vi.runAllTimersAsync(); await result.observed; vi.useRealTimers(); }
  });
  it('post-close log write stays inside the original owner budget without a reset deadline', async () => {
    vi.useFakeTimers();
    const ms = 180_000;
    const started = deferred<void>();
    const write = deferred<void>();
    const writer = settlement(write.promise);
    const owned = run(process.execPath, ['-e', 'process.stdout.write("short child")'], root,
      join(tmpdir(), `native-log-bound-${randomUUID()}.log`), undefined, ms,
      () => { started.resolve(); return write.promise; });
    const owner = settlement(owned.done);
    const result = settlement(finish(owned));
    try {
      // Consume spawn-owned time before delivering real close; a post-close reset is too late.
      vi.advanceTimersByTime(1_000);
      expect(await owned.closed).toEqual({ code: 0, signal: null });
      await started.promise;
      expect(owner.state.status).toBe('pending');
      await vi.advanceTimersByTimeAsync(ms + 5_000 - 1_000 - 1);
      expect(result.state.status).toBe('pending');
      await vi.advanceTimersByTimeAsync(2);
      expect(writer.state.status).toBe('pending');
      expect.soft(owner.state.status, 'done must bound post-close diagnostics too').toBe('rejected');
      expect.soft(result.state.status, 'no extra/reset deadline after child exit').toBe('rejected');
    } finally {
      write.resolve();
      await Promise.all([writer.observed, owner.observed, result.observed]);
      vi.useRealTimers();
      await killAndReap(owned);
    }
  });
  it('short real owner watchdog kills/reaps only its exact Node child', async () => {
    const directory = await mkdtemp(join(tmpdir(), 'native-owner-control-'));
    const sentinel = run(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], directory, join(directory, 'sentinel.log'), undefined, 10_000);
    const owned = run(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], directory, join(directory, 'owner.log'), undefined, 400);
    const start = performance.now();
    try {
      const owner = settlement(owned.done);
      const first = settlement(finish(owned));
      await pause(100);
      const second = settlement(finish(owned));
      await Promise.all([owner.observed, first.observed, second.observed]);
      expect(owner.state.status).toBe('rejected');
      expect(owner.state.error).toBeInstanceOf(Error);
      expect(String(owner.state.error)).toContain('child watchdog/output bound:');
      expect(first.state.error).toBe(owner.state.error);
      expect(second.state.error).toBe(owner.state.error);
      const closed = await owned.closed;
      expect(closed.code === null || closed.code !== 0).toBe(true);
      expect(owned.child.exitCode !== null || owned.child.signalCode !== null).toBe(true);
      expect(performance.now() - start).toBeGreaterThanOrEqual(300);
      expect(performance.now() - start).toBeLessThan(5_400);
      expect(alive(sentinel.child.pid!)).toBe(true);
    } finally { await killAndReap(owned); await killAndReap(sentinel); }
  });
});

async function archiveControl() {
  const c = await createCapsule(await mkdtemp(join(tmpdir(), 'native-archive-control-')));
  const source = c.target;
  const destination = `${source}.archive-${randomUUID()}`;
  const resources = platform === 'darwin' ? join(source, 'Contents/Resources') : join(source, 'resources');
  await mkdir(resources, { recursive: true });
  await writeFile(join(resources, 'identity.json'), JSON.stringify(c.marker));
  await writeFile(join(source, 'payload'), 'immutable seed bytes', { flag: 'wx' });
  const before = await inventory(source);
  const options = await seedOptions(c, source, destination);
  let elapsed = 0;
  const order: string[] = [];
  const sleeps: number[] = [];
  const trace: { attempt: number; elapsedMs: number; code?: string }[] = [];
  const verify = options.verify;
  const destinationExists = options.destinationExists;
  const sourceIsLink = options.sourceIsLink;
  Object.assign(options, {
    platform: 'win32', now: () => elapsed,
    sleep: async (ms: number) => {
      expect(ms).toBeGreaterThan(0); expect(ms).toBeLessThanOrEqual(100);
      sleeps.push(ms); elapsed += ms; order.push('sleep');
      if (elapsed > 5_000 || sleeps.length > 501) throw new Error('test safety fuse: unbounded retry');
    },
    verify: async () => { order.push('verify'); await verify(); },
    destinationExists: async () => { order.push('destination'); return destinationExists(); },
    sourceIsLink: async () => { order.push('link'); return sourceIsLink(); },
    observe: (event: { attempt: number; elapsedMs: number; code?: string }) => { trace.push(event); },
  } satisfies Partial<SeedArchiveOptions>);
  const fs = {
    sourceKind: async (path: string): Promise<'present' | 'missing'> => await present(path) ? 'present' : 'missing',
    rename: vi.fn(async (from: string, to: string) => { order.push('rename'); await rename(from, to); }),
  };
  return { c, source, destination, resources, before, options, fs, sleeps, trace, order, elapsed: () => elapsed,
    archive: () => archiveSeed({ source, destination }, fs, options),
    retained: async () => { assertSameInventory(before, await inventory(source)); expect(await present(destination)).toBe(false); },
  };
}
const sharingError = (code: string) => Object.assign(new Error(`rename ${code}`), { code });

describe.sequential('portable seed archive failure controls', () => {
  it.each(['EPERM', 'EACCES', 'EBUSY'])('Windows transient %s retries then moves real bytes exactly once', async (code) => {
    const x = await archiveControl();
    const failure = sharingError(code);
    x.fs.rename.mockImplementationOnce(async () => { x.order.push('rename'); throw failure; });
    await expect(x.archive()).resolves.toBeUndefined();
    expect(x.fs.rename).toHaveBeenCalledTimes(2);
    expect(x.fs.rename.mock.calls).toEqual([[x.source, x.destination], [x.source, x.destination]]);
    expect(x.sleeps).toHaveLength(1);
    expect(x.elapsed()).toBeLessThanOrEqual(100);
    expect(await present(x.source)).toBe(false);
    assertSameInventory(x.before, await inventory(x.destination));
    const attempts = x.order.reduce<number[]>((all, value, index) => value === 'rename' ? [...all, index] : all, []);
    for (const [index, at] of attempts.entries()) {
      const checks = x.order.slice(index === 0 ? 0 : attempts[index - 1]! + 1, at);
      expect(checks).toContain('verify'); expect(checks).toContain('destination');
    }
    expect(x.trace.filter((event) => event.code === code)).toHaveLength(1);
    expect(x.trace[0]).toMatchObject({ attempt: 1, elapsedMs: 0, code });
    expect(x.trace.every((event, index) => index === 0 || event.elapsedMs >= x.trace[index - 1]!.elapsedMs)).toBe(true);
  });
  it.each(['EPERM', 'EACCES', 'EBUSY'])('persistent %s exhausts at most 5s, retains final error and source', async (code) => {
    const x = await archiveControl();
    const errors: Error[] = [];
    x.fs.rename.mockImplementation(async () => { const error = sharingError(code); errors.push(error); throw error; });
    const result = settlement(x.archive());
    await result.observed;
    await x.retained();
    expect(result.state.status).toBe('rejected');
    expect(retainedErrors(result.state.error)).toContain(errors.at(-1));
    expect(x.fs.rename.mock.calls.length).toBeGreaterThan(1);
    expect(x.fs.rename.mock.calls.length).toBeLessThanOrEqual(501);
    expect(x.elapsed()).toBeGreaterThanOrEqual(4_900);
    expect(x.elapsed()).toBeLessThanOrEqual(5_000);
    expect(x.trace.at(-1)?.code).toBe(code);
  });
  it.each(['EIO', 'ENOENT', 'ENOSPC', 'EXDEV', 'EINVAL'])('does not retry other error %s', async (code) => {
    const x = await archiveControl();
    const error = sharingError(code);
    x.fs.rename.mockRejectedValue(error);
    await expect(x.archive()).rejects.toBe(error);
    expect(x.fs.rename).toHaveBeenCalledTimes(1); expect(x.sleeps).toEqual([]);
    await x.retained();
  });
  it.each(['darwin', 'linux'].flatMap((os) => ['EPERM', 'EACCES', 'EBUSY'].map((code) => [os, code] as const)))('does not retry sharing errors on %s: %s', async (os, code) => {
    const x = await archiveControl(); x.options.platform = os;
    const error = sharingError(code); x.fs.rename.mockRejectedValue(error);
    await expect(x.archive()).rejects.toBe(error);
    expect(x.fs.rename).toHaveBeenCalledTimes(1); expect(x.sleeps).toEqual([]);
    await x.retained();
  });
  it('refuses an existing empty destination instead of replacing it', async () => {
    const x = await archiveControl(); await mkdir(x.destination);
    const identity = await lstat(x.destination);
    const result = settlement(x.archive()); await result.observed;
    expect(result.state.status).toBe('rejected');
    expect(x.fs.rename).not.toHaveBeenCalled(); expect(x.sleeps).toEqual([]);
    expect((await lstat(x.destination)).ino).toBe(identity.ino);
    expect(await readdir(x.destination)).toEqual([]);
    assertSameInventory(x.before, await inventory(x.source));
  });
  it('destination appearing after sharing refusal prevents a second rename', async () => {
    const x = await archiveControl(); const error = sharingError('EBUSY');
    x.fs.rename.mockImplementationOnce(async () => { await mkdir(x.destination); throw error; });
    const result = settlement(x.archive()); await result.observed;
    expect(result.state.status).toBe('rejected');
    expect(x.order.filter((item) => item === 'destination').length).toBeGreaterThanOrEqual(2);
    expect(result.state.error).not.toBe(error);
    expect(x.fs.rename).toHaveBeenCalledTimes(1);
    expect(await readdir(x.destination)).toEqual([]);
    assertSameInventory(x.before, await inventory(x.source));
  });
  it.each(['capsule marker', 'app identity', 'directory identity', 'redirect'])('refuses changed %s during retry before another rename', async (change) => {
    const x = await archiveControl();
    const saved = `${x.source}.saved`;
    const external = join(x.c.root, 'external-owned');
    await mkdir(external); await writeFile(join(external, 'sentinel'), 'do not follow');
    const externalBefore = await inventory(external);
    x.fs.rename.mockImplementationOnce(async () => {
      if (change === 'capsule marker') await writeFile(x.c.markerPath, JSON.stringify({ ...x.c.marker, appId: 'wrong' }));
      else if (change === 'app identity') await writeFile(join(x.resources, 'identity.json'), '{}');
      else {
        await rename(x.source, saved);
        if (change === 'redirect') await symlink(external, x.source, platform === 'win32' ? 'junction' : 'dir');
        else await mkdir(x.source);
      }
      throw sharingError('EPERM');
    });
    const result = settlement(x.archive()); await result.observed;
    expect(result.state.status).toBe('rejected');
    expect(x.fs.rename).toHaveBeenCalledTimes(1);
    expect(await present(x.destination)).toBe(false);
    assertSameInventory(externalBefore, await inventory(external));
    if (change === 'directory identity' || change === 'redirect') assertSameInventory(x.before, await inventory(saved));
    expect(x.order.filter((item) => item === 'verify').length).toBeGreaterThanOrEqual(2);
    expect(retainedErrors(result.state.error).some((error) => error instanceof Error &&
      /invalid fixture capsule marker|seed app identity changed|seed source identity changed/.test(error.message))).toBe(true);
  });
  it.each(['verify', 'destinationExists', 'sourceIsLink'] as const)('preserves %s inspection error identity without rename/retry', async (guard) => {
    const x = await archiveControl();
    const error = sharingError('EACCES');
    const inspect = vi.fn(async (): Promise<never> => { throw error; });
    x.options[guard] = inspect;
    await expect(x.archive()).rejects.toBe(error);
    expect(inspect).toHaveBeenCalledTimes(1);
    expect(x.fs.rename).not.toHaveBeenCalled(); expect(x.sleeps).toEqual([]);
    await x.retained();
  });
  it('time spent in rename consumes the original 5s budget, never starts an attempt after expiry', async () => {
    const x = await archiveControl();
    let elapsed = 0;
    const attempts: number[] = [];
    const failures: Error[] = [];
    x.options.now = () => elapsed;
    const sleep = vi.fn(async (ms: number) => {
      expect(ms).toBeGreaterThan(0);
      expect(ms).toBeLessThanOrEqual(Math.min(100, 5_000 - elapsed));
      elapsed += ms;
    });
    x.options.sleep = sleep;
    x.fs.rename.mockImplementation(async () => {
      attempts.push(elapsed);
      expect(elapsed).toBeLessThan(5_000);
      elapsed += 5_000;
      const error = sharingError('EBUSY'); failures.push(error); throw error;
    });
    const result = settlement(x.archive()); await result.observed;
    expect(result.state.status).toBe('rejected');
    expect(retainedErrors(result.state.error)).toContain(failures.at(-1));
    expect(attempts).toEqual([0]);
    expect(elapsed).toBe(5_000);
    expect(sleep).not.toHaveBeenCalled();
    await x.retained();
  });
  it('first-attempt guard expiry awaits inspection but never starts rename', async () => {
    const x = await archiveControl();
    let elapsed = 0;
    x.options.now = () => elapsed;
    const verify = x.options.verify;
    x.options.verify = async () => { await verify(); elapsed = 4_900; };
    const entered = deferred<void>();
    const release = deferred<void>();
    const sourceIsLink = x.options.sourceIsLink;
    x.options.sourceIsLink = async () => {
      const link = await sourceIsLink();
      entered.resolve();
      await release.promise;
      return link;
    };
    const result = settlement(x.archive());
    try {
      await entered.promise;
      expect(elapsed).toBe(4_900);
      elapsed = 5_100;
      expect(result.state.status).toBe('pending');
      expect(x.fs.rename).not.toHaveBeenCalled();
    } finally { release.resolve(); await result.observed; }
    expect.soft(result.state.status).toBe('rejected');
    expect.soft(x.fs.rename).not.toHaveBeenCalled();
    expect(x.sleeps).toEqual([]);
    await x.retained();
  });
  it('retry guard expiry retains prior sharing error without a second rename', async () => {
    const x = await archiveControl();
    let elapsed = 0;
    let checks = 0;
    x.options.now = () => elapsed + x.elapsed();
    const verify = x.options.verify;
    x.options.verify = async () => { await verify(); if (++checks === 1) elapsed = 4_800; };
    const destinationExists = x.options.destinationExists;
    x.options.destinationExists = async () => {
      const exists = await destinationExists();
      if (checks === 2) {
        expect(x.options.now()).toBe(4_900);
        elapsed += 200;
      }
      return exists;
    };
    const failure = sharingError('EBUSY');
    x.fs.rename.mockRejectedValueOnce(failure);
    const result = settlement(x.archive()); await result.observed;
    expect.soft(result.state.status).toBe('rejected');
    expect.soft(result.state.error).toBe(failure);
    expect.soft(x.fs.rename).toHaveBeenCalledTimes(1);
    expect(x.sleeps).toEqual([100]);
    expect(checks).toBe(2);
    await x.retained();
  });
  it('real seedOptions sourceIsLink refuses a redirect observed after successful verify', async () => {
    const x = await archiveControl();
    const saved = `${x.source}.saved`;
    const external = join(x.c.root, 'late-external-owned');
    await mkdir(external); await writeFile(join(external, 'sentinel'), 'do not follow');
    const externalBefore = await inventory(external);
    await expect(x.options.verify()).resolves.toBeUndefined();
    await rename(x.source, saved);
    await symlink(external, x.source, platform === 'win32' ? 'junction' : 'dir');
    await expect.soft(x.options.sourceIsLink()).rejects.toThrow();
    expect(x.fs.rename).not.toHaveBeenCalled();
    expect((await lstat(x.source)).isSymbolicLink()).toBe(true);
    expect(await present(x.destination)).toBe(false);
    assertSameInventory(x.before, await inventory(saved));
    assertSameInventory(externalBefore, await inventory(external));
  });
  it('archive refuses a late redirect observed by real seedOptions before mutation', async () => {
    const x = await archiveControl();
    const saved = `${x.source}.saved`;
    const external = join(x.c.root, 'late-external-owned');
    await mkdir(external); await writeFile(join(external, 'sentinel'), 'do not follow');
    const externalBefore = await inventory(external);
    const destinationExists = x.options.destinationExists;
    x.options.destinationExists = async () => {
      expect(x.order).toEqual(['verify']);
      await rename(x.source, saved);
      await symlink(external, x.source, platform === 'win32' ? 'junction' : 'dir');
      return destinationExists();
    };
    const result = settlement(x.archive()); await result.observed;
    expect.soft(result.state.status).toBe('rejected');
    expect.soft(x.fs.rename).not.toHaveBeenCalled();
    expect.soft(await present(x.destination)).toBe(false);
    expect.soft(await present(x.source)).toBe(true);
    expect(x.order).toContain('link');
    expect(x.sleeps).toEqual([]);
    assertSameInventory(x.before, await inventory(saved));
    assertSameInventory(externalBefore, await inventory(external));
  });
  it('an arbitrary sibling is not an owned target/backup source', async () => {
    const x = await archiveControl();
    const sibling = join(x.c.root, 'not-a-seed'); await rename(x.source, sibling);
    const options = await seedOptions(x.c, sibling, x.destination);
    await expect(archiveSeed({ source: sibling, destination: x.destination }, x.fs, options)).rejects.toThrow('unowned seed source');
    expect(x.fs.rename).not.toHaveBeenCalled();
    expect(await present(x.destination)).toBe(false);
    assertSameInventory(x.before, await inventory(sibling));
  });
  it('initial invalid capsule refuses even an otherwise successful rename', async () => {
    const x = await archiveControl();
    await writeFile(x.c.markerPath, '{}');
    const result = settlement(x.archive()); await result.observed;
    expect(result.state.status).toBe('rejected'); expect(x.fs.rename).not.toHaveBeenCalled();
    await x.retained();
  });
  it.each([false, true])('pre-existing owned link moves only the LINK, no retries (sharing refusal=%s)', async (refuse) => {
    const x = await archiveControl();
    const saved = `${x.source}.saved`; await rename(x.source, saved);
    await symlink(saved, x.source, platform === 'win32' ? 'junction' : 'dir');
    const options = await seedOptions(x.c, x.source, x.destination);
    options.platform = 'win32'; options.sleep = x.options.sleep;
    const failure = sharingError('EPERM');
    if (refuse) x.fs.rename.mockRejectedValue(failure);
    const work = archiveSeed(x, x.fs, options);
    if (refuse) {
      await expect(work).rejects.toBe(failure);
      expect((await lstat(x.source)).isSymbolicLink()).toBe(true);
      expect(await present(x.destination)).toBe(false);
    } else {
      await expect(work).resolves.toBeUndefined();
      expect((await lstat(x.destination)).isSymbolicLink()).toBe(true);
      expect(await present(x.source)).toBe(false);
    }
    expect(x.fs.rename).toHaveBeenCalledTimes(1); expect(x.sleeps).toEqual([]);
    assertSameInventory(x.before, await inventory(saved));
  });
  it('initially absent seed stays a no-op but rejects a source appearing before verification', async () => {
    const x = await archiveControl();
    const saved = `${x.source}.saved`; await rename(x.source, saved);
    const options = await seedOptions(x.c, x.source, x.destination);
    await expect(options.verify()).resolves.toBeUndefined();
    await expect(archiveSeed(x, x.fs, options)).resolves.toBeUndefined();
    expect(x.fs.rename).not.toHaveBeenCalled(); expect(x.sleeps).toEqual([]);
    await rename(saved, x.source);
    await expect(options.verify()).rejects.toThrow('seed source appeared after initial absence');
    await x.retained();
  });
  it('missing seed is a no-op without rename or sleep', async () => {
    const x = await archiveControl(); await rename(x.source, `${x.source}.saved`);
    const options = await seedOptions(x.c, x.source, x.destination);
    options.sleep = x.options.sleep;
    await expect(archiveSeed(x, x.fs, options)).resolves.toBeUndefined();
    expect(x.fs.rename).not.toHaveBeenCalled(); expect(x.sleeps).toEqual([]);
    expect(await present(x.destination)).toBe(false);
    assertSameInventory(x.before, await inventory(`${x.source}.saved`));
  });
  it('loss of initially captured source refuses before rename or install', async () => {
    const x = await archiveControl();
    const saved = `${x.source}.saved`; await rename(x.source, saved);
    const install = vi.fn();
    const result = settlement(x.archive().then(install)); await result.observed;
    expect.soft(result.state.status).toBe('rejected');
    expect.soft(install).not.toHaveBeenCalled();
    expect(x.fs.rename).not.toHaveBeenCalled(); expect(x.sleeps).toEqual([]);
    expect(await present(x.source)).toBe(false);
    expect(await present(x.destination)).toBe(false);
    assertSameInventory(x.before, await inventory(saved));
  });
  it('invalid capsule with initially absent source refuses before rename or install', async () => {
    const x = await archiveControl();
    const saved = `${x.source}.saved`; await rename(x.source, saved);
    const options = await seedOptions(x.c, x.source, x.destination);
    options.sleep = x.options.sleep;
    await writeFile(x.c.markerPath, '{}');
    const install = vi.fn();
    const result = settlement(archiveSeed(x, x.fs, options).then(install)); await result.observed;
    expect.soft(result.state.status).toBe('rejected');
    expect.soft(install).not.toHaveBeenCalled();
    expect(x.fs.rename).not.toHaveBeenCalled(); expect(x.sleeps).toEqual([]);
    expect(await present(x.source)).toBe(false);
    expect(await present(x.destination)).toBe(false);
    assertSameInventory(x.before, await inventory(saved));
  });
});

describe.sequential('Windows exclusive-reader archive control', () => {
  it.skipIf(platform !== 'win32')('real CreateFileW deny-delete handle: witnessed refusal, ACK release, real archive', async () => {
    const x = await archiveControl();
    readerEvidence.push(x.c);
    const ready = join(x.c.witnessDir, 'reader.ready');
    const ack = join(x.c.witnessDir, 'reader.release');
    const quote = (value: string) => `'${value.replaceAll("'", "''")}'`;
    const script = `
$ErrorActionPreference = 'Stop'
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using Microsoft.Win32.SafeHandles;
public static class DeleteDenyReader {
  [DllImport("kernel32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
  public static extern SafeFileHandle CreateFileW(string name, uint access, uint share, IntPtr security, uint disposition, uint flags, IntPtr template);
}
'@
# Decimal UInt32 keeps GENERIC_READ unsigned in Windows PowerShell 5.1.
$handle = [DeleteDenyReader]::CreateFileW(${quote(x.source)}, [uint32]2147483648, 3, [IntPtr]::Zero, 3, 0x02000000, [IntPtr]::Zero)
if ($handle.IsInvalid) { throw "CreateFileW failed: $([Runtime.InteropServices.Marshal]::GetLastWin32Error())" }
try {
  [IO.File]::WriteAllText(${quote(ready)}, 'handle-acquired-no-FILE_SHARE_DELETE')
  $clock = [Diagnostics.Stopwatch]::StartNew()
  while (-not [IO.File]::Exists(${quote(ack)})) {
    if ($clock.ElapsedMilliseconds -ge 15000) { throw 'reader ACK watchdog' }
    Start-Sleep -Milliseconds 10
  }
} finally { $handle.Dispose() }
`;
    const reader = run('powershell.exe', ['-NoProfile', '-NonInteractive', '-EncodedCommand',
      Buffer.from(script, 'utf16le').toString('base64')], x.c.root, join(x.c.witnessDir, 'reader.log'), undefined, 25_000);
    const readerResult = settlement(reader.done);
    const release = async () => { if (!await present(ack)) await writeFile(ack, 'release', { flag: 'wx' }); };
    const errors: unknown[] = [];
    try {
      await until(async () => {
        if (await present(ready)) return true;
        if (readerResult.state.status !== 'pending') throw new Error(`reader exited before readiness: ${String(readerResult.state.error)}`);
        return false;
      }, 'owned exclusive reader readiness', 10_000);
      expect(await readFile(ready, 'utf8')).toBe('handle-acquired-no-FILE_SHARE_DELETE');
      const oneShot = settlement(rename(x.source, x.destination));
      await oneShot.observed;
      await writeFile(join(x.c.witnessDir, 'reader-observation.json'), JSON.stringify({
        capsule: x.c.root, source: x.source, ready: await readFile(ready, 'utf8'),
        status: oneShot.state.status, code: (oneShot.state.error as NodeJS.ErrnoException | undefined)?.code,
        beforeACK: true,
      }));
      expect(oneShot.state.status).toBe('rejected');
      expect(['EPERM', 'EACCES', 'EBUSY']).toContain((oneShot.state.error as NodeJS.ErrnoException).code);
      await x.retained();
      const options = await seedOptions(x.c, x.source, x.destination);
      const trace: { attempt: number; elapsedMs: number; code?: string }[] = [];
      let released = false;
      options.observe = async (event) => {
        trace.push(event);
        if (!released && event.code !== undefined) {
          expect(event.attempt).toBe(1);
          expect(['EPERM', 'EACCES', 'EBUSY']).toContain(event.code);
          expect(readerResult.state.status).toBe('pending');
          released = true;
          await release();
          expect((await bounded(reader.done, 'reader ACK close', 5_000)).code).toBe(0);
          expect((await reader.closed).code).toBe(0);
        }
      };
      const start = performance.now();
      try {
        await expect(archiveSeed(x, x.fs, options)).resolves.toBeUndefined();
      } finally {
        await writeFile(join(x.c.witnessDir, 'archive-trace.json'), JSON.stringify(trace));
      }
      expect(released).toBe(true);
      expect(x.fs.rename.mock.calls.length).toBeGreaterThanOrEqual(2);
      expect(performance.now() - start).toBeLessThanOrEqual(5_000);
      expect(trace.some((event) => event.code !== undefined)).toBe(true);
      expect(trace.every((event, index) => index === 0 || event.elapsedMs >= trace[index - 1]!.elapsedMs)).toBe(true);
      expect(await present(x.source)).toBe(false);
      assertSameInventory(x.before, await inventory(x.destination));
    } catch (error) { errors.push(error); }
    // ACK and actual close on both RED and GREEN. Only exact tracked child may be killed on cleanup failure.
    try {
      await release();
      expect((await bounded(reader.done, 'reader final ACK close', 5_000)).code).toBe(0);
      expect((await reader.closed).code).toBe(0);
    } catch (error) {
      errors.push(error);
      try { await killAndReap(reader); } catch (cleanup) { errors.push(cleanup); }
    }
    await readerResult.observed;
    if (errors.length) throw new AggregateError(errors, `exclusive reader control; retained ${x.c.root}`);
  }, 35_000);
});

describe('portable registry observation controls', () => {
  const target = 'C:\\native update テスト-mXSZpZ\\fixture';
  const valid = { InstallLocation: target, DisplayVersion: '1.0.0' };
  it('accepts the exact Unicode location and version', () => {
    checkRegistryObservation(JSON.stringify(valid), target, '1.0.0');
  });
  it.each([
    { ...valid, InstallLocation: target.replace('テスト', '???') },
    { ...valid, InstallLocation: `${target}-other` },
    { ...valid, DisplayVersion: '1.0.1' },
    { ...valid, InstallLocation: '' },
    { ...valid, DisplayVersion: null },
    { InstallLocation: target },
    { ...valid, extra: true },
    [],
  ])('rejects mismatched or invalid fields: %j', (value) => {
    expect(() => checkRegistryObservation(JSON.stringify(value), target, '1.0.0')).toThrow();
  });
  it.each([
    'not JSON',
    `${JSON.stringify(valid)}\n${JSON.stringify(valid)}`,
    JSON.stringify(valid).replace('{', '{"InstallLocation":"wrong",'),
    JSON.stringify(valid).replace('}', ',"DisplayVersion":"1.0.0"}'),
  ])('rejects malformed/duplicate observations: %s', (text) => {
    expect(() => checkRegistryObservation(text, target, '1.0.0')).toThrow();
  });
});
async function conservation(x: Context) {
  for (const [file, digest] of x.s.sentinels) expect(await hash(file), file).toBe(digest);
  expect(alive(x.s.sentinel.child.pid!)).toBe(true);
  if (host === 'win') expect(await hash(x.s.n.artifact)).toBe(x.s.nHash);
  else assertSameInventory(x.s.immutable, await inventory(x.s.n.artifact));
}
async function success(x: Context, h: Running) {
  const boot = await attempt(x, 'normal');
  const receipt = await validateReceipt(x.s.c, x.id, '1.0.1', x.s.n1.marker);
  expect(receipt.pid).toBe(boot.pid);
  expect(receipt.pid).not.toBe(x.pre.pid);
  expect(alive(x.pre.pid)).toBe(false);
  expect(alive(boot.pid)).toBe(true);
  const completed = await finish(h);
  expect(completed.code).toBe(0);
  expect(completed.events.filter((event) => event.event === 'barrier')).toEqual(
    barriers.map((name) => ({ v: 1, event: 'barrier', transactionId: x.id, name })),
  );
  expect(await realpath(receipt.execPath)).toBe(paths(x.s.c).execPath);
  expect(await readFile(join(paths(x.s.c).resourcesPath, 'full-resource-marker.txt'), 'utf8')).toBe(x.s.n1.marker);
  if (host === 'mac') assertSameInventory(await inventory(x.s.n1.artifact), await inventory(x.s.c.target));
  else {
    await registry(x, '1.0.1');
    const sandbox = await inventory(join(x.s.c.root, 'sandbox'));
    expect(sandbox.filter((entry) => entry.kind === 'file' && entry.path.endsWith(`${x.s.c.marker.executableName}.exe`)),
      'no private HOME default-path installation').toEqual([]);
    expect(await present(join(x.s.c.root, x.s.c.marker.productName)), 'no sibling product target').toBe(false);
  }
  const witnessed = await bootInventory(x.s.c);
  const owned = witnessed.filter((entry) => entry.value.transactionId === x.id);
  expect(owned).toHaveLength(1);
  expect(owned[0]!.value).toEqual(boot);
  expect(witnessed.filter((entry) => entry.value.transactionId !== x.id)).toEqual(x.boots);
  await pause(500);
  expect(alive(boot.pid), 'N1 must remain alive until our own exit file').toBe(true);
  expect(await bootInventory(x.s.c)).toEqual(witnessed);
  await stopAttempt(x, x.id);
}
async function bootInventory(c: NativeFixtureCapsule) {
  const files = (await readdir(c.receiptDir)).filter((file) => file.includes('.boot.json.attempt-') && file.endsWith('.json')).sort();
  return Promise.all(files.map(async (file) => ({ file, value: JSON.parse(await readFile(join(c.receiptDir, file), 'utf8')) as BootAttempt })));
}
async function retainedBeforeRecovery(x: Context, variant: PackagedVariant) {
  expect(await readFile(join(paths(x.s.c).resourcesPath, 'full-resource-marker.txt'), 'utf8')).toBe(variant.marker);
  expect(JSON.parse(await readFile(join(paths(x.s.c).resourcesPath, 'fixture-mode.json'), 'utf8'))).toEqual({ mode: variant.mode });
  if (host === 'mac') {
    assertSameInventory(x.s.immutable, await inventory(join(x.s.c.root, 'backup.app')));
    assertSameInventory(await inventory(variant.artifact), await inventory(x.s.c.target));
  } else {
    expect(await hash(x.s.n.artifact)).toBe(x.s.nHash);
    await registry(x, '1.0.1');
  }
}
async function restored(x: Context) {
  if (host === 'mac') assertSameInventory(x.s.immutable, await inventory(x.s.c.target));
  else {
    // NSIS may regenerate its uninstaller. All immutable installed app resources
    // and executable bytes must nonetheless match the independently seeded N.
    const appFiles = (entries: FileInventory[]) => entries.filter((e) => !e.path.includes('Uninstall '));
    assertSameInventory(appFiles(x.before), appFiles(await inventory(x.s.c.target)));
    await registry(x, '1.0.0');
    expect(await hash(x.s.n.artifact)).toBe(x.s.nHash);
  }
  await manualN(x, 'recovery-witness');
}
async function recoverTwice(x: Context) {
  await stopAttempt(x, x.id);
  for (let i = 0; i < 2; i++) {
    const beforeBoots = await bootInventory(x.s.c);
    const beforeBarriers = (await readdir(x.s.c.barrierDir)).sort();
    const value = request(x);
    value.op = 'recover';
    delete value.to;
    const result = await finish(invoke(x, value));
    expect(result.code).toBe(0);
    expect(result.events.filter((e) => e.event === 'barrier')).toEqual([]);
    expect((await readdir(x.s.c.barrierDir)).sort()).toEqual(beforeBarriers);
    expect(await bootInventory(x.s.c), 'recover must not bootstrap/retry automatically').toEqual(beforeBoots);
    expect(await present(receiptPath(x.s.c, x.id))).toBe(false);
    await restored(x); // a fresh manual ID/receipt/real N process after EACH recover
    await conservation(x);
  }
}
async function refused(x: Context, h: Running, id = x.id) {
  const result = await finish(h);
  expect(result.code).not.toBe(0);
  expect(result.code).not.toBeNull();
  expect(result.events.filter((e) => e.event === 'barrier')).toEqual([]);
  expect(await bootInventory(x.s.c)).toEqual(x.boots);
  expect(await present(barrierPath(x, id, 'before-mutation'))).toBe(false);
  expect(await present(bootPath(x.s.c, id))).toBe(false);
  expect(await present(receiptPath(x.s.c, id))).toBe(false);
  assertSameInventory(x.before, await inventory(x.s.c.target));
}
async function cleanupContext(x: Context) {
  // Reap all helpers first so none can launch another fixture during cleanup.
  const errors: unknown[] = [];
  for (const h of x.helpers) {
    try { await killAndReap(h); } catch (error) { errors.push(error); }
  }
  for (const id of x.transactions) {
    try { await stopAttempt(x, id); } catch (error) { errors.push(error); }
  }
  for (const app of x.manual) {
    try { await app.stop(); await until(() => !alive(app.pid), 'manual cleanup'); }
    catch (error) { errors.push(error); }
  }
  if (errors.length) throw new AggregateError(errors, `uncertain ownership; retained ${x.s.c.root}`);
}
function nativeCase(name: string, body: (x: Context) => Promise<void>, os?: 'mac' | 'win', live = false) {
  it.skipIf(!enabled || !host || (os !== undefined && host !== os))(name, async () => {
    // Every native case asserts entrypoint BEFORE lazy fixture/build setup.
    const s = await ready();
    let x: Context | undefined;
    const errors: unknown[] = [];
    try {
      x = await seed(s, live);
      await body(x);
      await conservation(x);
    } catch (error) { errors.push(error); }
    if (x) {
      try { await cleanupContext(x); }
      catch (error) { errors.push(error); }
    }
    if (errors.length) {
      poisoned = true;
      if (errors.length === 1) throw errors[0];
      throw new AggregateError(errors, 'native case and cleanup failed');
    }
  }, 16 * 60_000);
}

describe.sequential('S0 real native replacement (off by default; not production qualification)', () => {
  it('explicitly reports the missing Go entrypoint, without importing/building it', () => {
    expect(existsSync(entrypoint), `missing helper entrypoint: ${entrypoint}`).toBe(true);
  });
  it.skipIf(!enabled)('rejects native opt-in on unsupported OS/architecture', () => {
    expect(host, 'requires macOS arm64/x64 or Windows x64; all-skipped is not qualification').toBeDefined();
  });

  nativeCase('01 real N → N1, ACK every barrier, exact tree/path/registry and owned exit', async (x) => {
    expect(x.s.c.target).toContain('native update テスト-');
    const h = invoke(x, request(x));
    await drive(x, h);
    await success(x, h);
  });

  nativeCase('02 identity mismatch alone refuses before mutation on either host', async (x) => {
    const value = request(x);
    value.identity = 'org.voidcode.fixture.other';
    await refused(x, invoke(x, value));
  });

  nativeCase('03 actual target symlink/junction escape preserves the external owned fixture', async (x) => {
    // All request fields stay valid. Only the exact target becomes a redirect.
    const external = await mkdtemp(join(tmpdir(), 'native external sentinel テスト-'));
    const externalTarget = join(external, 'fixture');
    await cp(x.s.c.target, externalTarget, { recursive: true, verbatimSymlinks: true, preserveTimestamps: true });
    await writeFile(join(external, 'sentinel.txt'), 'external-owned-do-not-touch', { flag: 'wx' });
    const externalBefore = await inventory(external);
    await rename(x.s.c.target, `${x.s.c.target}.escape-original-${x.id}`);
    await symlink(externalTarget, x.s.c.target, host === 'win' ? 'junction' : 'dir');
    expect((await lstat(x.s.c.target)).isSymbolicLink()).toBe(true);
    expect(await realpath(x.s.c.target)).toBe(await realpath(externalTarget));
    await refused(x, invoke(x, request(x)));
    assertSameInventory(externalBefore, await inventory(external));
    expect((await lstat(x.s.c.target)).isSymbolicLink()).toBe(true);
    // Preserve the external evidence too; do not follow/delete a redirected target.
    await writeFile(join(x.s.c.witnessDir, `${x.id}-external.txt`), external);
  });

  nativeCase('04 live recorded N is actually alive and byte-unchanged after refusal', async (x) => {
    expect(alive(x.pre.pid)).toBe(true);
    await refused(x, invoke(x, request(x)));
    expect(alive(x.pre.pid)).toBe(true);
    await pause(300);
    expect(alive(x.pre.pid)).toBe(true);
  }, undefined, true);

  nativeCase('05 first holds before-mutation lock, second is inert, first completes real N1', async (x) => {
    const first = invoke(x, request(x));
    await observeBarrier(x, first, x.id, 'before-mutation');
    const secondId = `${x.id}-contender`;
    await refused(x, invoke(x, request(x, x.s.n1, secondId)), secondId);
    expect(alive(first.child.pid!)).toBe(true);
    await ack(x, x.id, 'before-mutation');
    await drive(x, first, x.s.n1, x.id, 1);
    await success(x, first);
    expect(await present(bootPath(x.s.c, secondId))).toBe(false);
    expect((await bootInventory(x.s.c)).filter((entry) => entry.value.transactionId === secondId)).toEqual([]);
  });

  nativeCase('06 immutable wrong-token app writes a real wrong receipt; recovery boots N twice', async (x) => {
    const h = invoke(x, request(x, x.s.wrong));
    await drive(x, h, x.s.wrong);
    const boot = await attempt(x, 'wrong-token');
    const wrong = await validateReceipt(x.s.c, `${x.id}-wrong`, '1.0.1', x.s.wrong.marker);
    expect(wrong.pid).toBe(boot.pid);
    expect(alive(boot.pid)).toBe(true);
    expect(await present(receiptPath(x.s.c, x.id))).toBe(false);
    // App ownership stays with A; helper must not kill/roll back a running bad boot.
    await stopAttempt(x, x.id);
    expect((await finish(h)).code).not.toBe(0);
    await retainedBeforeRecovery(x, x.s.wrong);
    await recoverTwice(x);
  });

  nativeCase('07 immutable missing-receipt app really boots/exits; recovery boots N twice', async (x) => {
    const h = invoke(x, request(x, x.s.missing));
    await drive(x, h, x.s.missing);
    const boot = await attempt(x, 'missing');
    await until(() => !alive(boot.pid), 'missing variant naturally exits without receipt');
    expect((await finish(h)).code).not.toBe(0);
    const ledger = await bootInventory(x.s.c);
    expect(ledger.filter((entry) => entry.value.transactionId !== x.id)).toEqual(x.boots);
    const owned = ledger.filter((entry) => entry.value.transactionId === x.id);
    expect(owned).toHaveLength(1);
    expect(owned[0]!.value).toEqual(boot);
    await pause(500);
    expect(await bootInventory(x.s.c)).toEqual(ledger);
    await retainedBeforeRecovery(x, x.s.missing);
    expect(await present(receiptPath(x.s.c, x.id))).toBe(false);
    expect(await present(receiptPath(x.s.c, `${x.id}-wrong`))).toBe(false);
    await recoverTwice(x);
  });

  for (const [index, selected] of ['before-mutation', 'after-old-to-backup', 'after-new-to-target-before-commit'].entries()) {
    nativeCase(`${8 + index} SIGKILL actual helper at ${selected}; fresh recovery manually boots intact N twice`, async (x) => {
      const h = invoke(x, request(x));
      for (const name of barriers) {
        await observeBarrier(x, h, x.id, name);
        if (name === selected) break;
        await ack(x, x.id, name);
      }
      await killAndReap(h);
      expect((await finish(h)).code).toBeNull();
      expect((await h.closed).signal).toBe('SIGKILL');
      expect(alive(h.child.pid!)).toBe(false);
      expect(await present(barrierPath(x, x.id, selected).replace(/\.json$/, '.continue'))).toBe(false);
      expect(await present(bootPath(x.s.c, x.id))).toBe(false);
      expect(await bootInventory(x.s.c)).toEqual(x.boots);
      await recoverTwice(x);
    }, 'mac');
  }

  nativeCase('11 real NSIS partial mutation is damaged before recovery; retained N restores files/HKCU/boot', async (x) => {
    expect(x.s.fault, 'Windows must package the actual NSIS fault variant').toBeDefined();
    const h = invoke(x, request(x, x.s.fault!));
    await drive(x, h, x.s.fault!);
    const witness = join(x.s.c.witnessDir, 'partial-install.json');
    await until(() => present(witness), 'external NSIS customInstall witness');
    const result = await finish(h);
    expect(result.code).not.toBe(0);
    expect(result.code).not.toBeNull();
    expect(JSON.parse(await readFile(witness, 'utf8'))).toEqual({ v: 1, fault: 'post-extraction' });
    expect(await present(join(paths(x.s.c).resourcesPath, 'full-resource-marker.txt'))).toBe(false);
    await registry(x, '1.0.1'); // actual extraction/registration happened before the fault
    expect(await present(bootPath(x.s.c, x.id))).toBe(false);
    expect(await hash(x.s.n.artifact)).toBe(x.s.nHash);
    expect(await bootInventory(x.s.c)).toEqual(x.boots);
    await recoverTwice(x);
  }, 'win');

  nativeCase('12 repeated fresh recovery of a before-mutation interruption never launches/retries', async (x) => {
    const h = invoke(x, request(x));
    await observeBarrier(x, h, x.id, 'before-mutation');
    await killAndReap(h);
    expect(alive(h.child.pid!)).toBe(false);
    expect(await bootInventory(x.s.c)).toEqual(x.boots);
    await recoverTwice(x);
  });
});

afterAll(async () => {
  const errors: unknown[] = [];
  for (const x of contexts) {
    try { await cleanupContext(x); } catch (error) { errors.push(error); }
  }
  for (const app of manualChildren) {
    try { await app.stop(); } catch (error) { errors.push(error); }
  }
  for (const child of children) {
    try { await killAndReap(child); } catch (error) { errors.push(error); }
  }
  for (const control of readerEvidence) {
    try {
      const output = join(root, 'artifacts/native-update', `exclusive-reader-${platform}-${randomUUID()}`);
      await retainEvidence(control.witnessDir, output, /^(?:reader\.log|reader\.ready|reader\.release|reader-observation\.json|archive-trace\.json)$/);
    } catch (error) { errors.push(error); }
  }
  if (capsule) {
    const output = join(root, 'artifacts/native-update', `${platform}-${arch}-${randomUUID()}`);
    await mkdir(output, { recursive: true });
    await writeFile(join(output, 'capsule.txt'), capsule.root);
    for (const directory of [capsule.witnessDir, capsule.receiptDir, capsule.barrierDir]) {
      for (const file of await readdir(directory)) {
        if (/\.(?:log|json|txt)$/.test(file)) {
          await cp(join(directory, file), join(output, file), { errorOnExist: true, force: false });
        }
      }
    }
    // Journal records are opaque; never traverse staged .new.app bundles.
    await retainEvidence(capsule.journalDir, join(output, 'journals'), /\.json$/);
    await writeFile(join(output, 'cleanup.json'), JSON.stringify({
      retained: true, capsule: capsule.root, errors: errors.map(String),
      observationScope: 'Immutable bootstrap ledger; all validated witnessed PIDs must disappear via transaction exit files. Native qualification requires actual runs.',
    }, null, 2));
  }
  // Conservatively retain diagnostics/packages and own registry residue.
  // Retention does not excuse lingering witnessed children; cleanup errors fail.
  if (errors.length) throw new AggregateError(errors, `native cleanup uncertain; retained ${capsule?.root}`);
}, 5 * 60_000);

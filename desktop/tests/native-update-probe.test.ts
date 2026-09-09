import { spawn } from 'node:child_process';
import type { ChildProcess } from 'node:child_process';
import { createHash, randomUUID } from 'node:crypto';
import { existsSync } from 'node:fs';
import { cp, lstat, mkdir, mkdtemp, readFile, readdir, realpath, rename, symlink, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve } from 'node:path';
import { arch, platform } from 'node:process';
import { afterAll, describe, expect, it } from 'vitest';
import {
  assertSameInventory, copyInitialMac, createCapsule, installInitialNsis, inventory,
  launchFixture, packageVariant, readAndValidateBootAttempt, readAndValidateReceipt, registryWitness,
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
function run(command: string, args: string[], cwd: string, log: string, input?: Request, ms = deadlineMs): Running {
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
  const done = (async () => {
    try {
      const result = await bounded(closed, `reap ${command}`, ms + 5_000);
      const text = Buffer.concat(stdout).toString('utf8');
      await writeFile(log, `stdout:\n${text}\nstderr:\n${Buffer.concat(stderr).toString('utf8')}`);
      if (expired || overflow) throw new Error(`child watchdog/output bound: ${command}; ${log}`);
      if (stdinError) throw stdinError;
      const events = input && text.trim() ? text.trim().split(/\r?\n/).map((line) => JSON.parse(line) as Event) : [];
      if (input) for (const event of events) {
        expect(event.v).toBe(1);
        expect(['barrier', 'started', 'refused', 'failed', 'exited']).toContain(event.event);
      }
      return { code: result.code, events };
    } finally { clearTimeout(timer); }
  })();
  // Early spawn/watchdog errors remain rejected for the caller without becoming unhandled.
  void done.then(() => undefined, () => undefined);
  void closed.then(() => undefined, () => undefined);
  const running = { child, closed, done };
  children.push(running);
  return running;
}
async function finish(running: Running) {
  try { return await bounded(running.done, 'helper operation completion'); }
  catch (error) { await killAndReap(running); throw error; }
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
  for (const path of [s.c.target, join(s.c.root, 'backup.app')]) {
    if (await present(path)) await rename(path, `${path}.archive-${id}`);
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
async function registry(x: Context, version: string) {
  const witness = registryWitness(x.s.c.marker.registryGuid);
  const query = async (key: string, value: string) => {
    const output = await tool(x.s, 'reg.exe', ['query', key, '/v', value]);
    const values = output.split(/\r?\n/).map((line) => /^\s*(\S+)\s+REG_SZ\s+(.*?)\s*$/.exec(line))
      .filter((match) => match !== null && match[1] === value);
    expect(values, `exact own HKCU ${key}/${value}`).toHaveLength(1);
    return values[0]![2];
  };
  // Pinned installer.nsh writes location to the install key, version to uninstall.
  expect(await query(witness.installKey, witness.installLocationValue)).toBe(x.s.c.target);
  expect(await query(witness.uninstallKey, witness.displayVersionValue)).toBe(version);
}
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
    await writeFile(join(output, 'cleanup.json'), JSON.stringify({
      retained: true, capsule: capsule.root, errors: errors.map(String),
      observationScope: 'Immutable bootstrap ledger; all validated witnessed PIDs must disappear via transaction exit files. Native qualification requires actual runs.',
    }, null, 2));
  }
  // Conservatively retain diagnostics/packages and own registry residue.
  // Retention does not excuse lingering witnessed children; cleanup errors fail.
  if (errors.length) throw new AggregateError(errors, `native cleanup uncertain; retained ${capsule?.root}`);
}, 5 * 60_000);

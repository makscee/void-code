import { execFile, execFileSync } from 'node:child_process';
import { createRequire } from 'node:module';
import { cp, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

const mac = process.platform === 'darwin' && process.arch === 'arm64';
const windows = process.platform === 'win32' && process.arch === 'x64';
if (!mac && !windows) throw new Error('packaged startup failure check supports macOS-arm64 and Windows-x64');
const defaultApp = mac ? 'release/mac-arm64/Void Code.app' : 'release/win-unpacked';
const source = process.env.VOID_PACKAGED_APP ? path.resolve(process.env.VOID_PACKAGED_APP) : path.resolve(defaultApp);
const require = createRequire(import.meta.url);
const asar = require('@electron/asar');
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const secret = 'vc-token-must-not-appear-7f4d2a';
const results = [];
const powershell = windows ? path.join(process.env.SystemRoot ?? 'C:\\Windows', 'System32/WindowsPowerShell/v1.0/powershell.exe') : undefined;
function inventory(app) {
  if (mac) return execFileSync('/bin/ps', ['-axo', 'pid=,command='], { encoding: 'utf8' }).split('\n').filter((line) => line.includes(app));
  const literal = app.replaceAll("'", "''");
  const output = execFileSync(powershell, ['-NoProfile', '-NonInteractive', '-Command', `@(Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -like '${literal}*' } | Select-Object ProcessId,CommandLine) | ConvertTo-Json -Compress`], { encoding: 'utf8' }).trim();
  if (!output) return []; const parsed = JSON.parse(output); return Array.isArray(parsed) ? parsed : [parsed];
}

async function run(name, mutate, expected) {
  const root = await mkdtemp(path.join(os.tmpdir(), `void-${name}-`));
  const app = path.join(root, mac ? 'Void Code.app' : 'Void Code');
  const userData = path.join(root, 'user-data');
  if (mac) execFileSync('/usr/bin/ditto', [source, app]); else await cp(source, app, { recursive: true });
  await mutate(app);
  const binary = mac ? path.join(app, 'Contents/MacOS/Void Code') : path.join(app, 'Void Code.exe');
  const child = execFile(binary, [`--user-data-dir=${userData}`, '--void-startup-test-no-dialog'], { env: { HOME: process.env.HOME, USERPROFILE: process.env.USERPROFILE, TEMP: process.env.TEMP, TMPDIR: process.env.TMPDIR ?? '/tmp', PATH: mac ? '/usr/bin:/bin' : `${process.env.SystemRoot}\\System32`, VOID_TEST_TOKEN: secret, ELECTRON_RUN_AS_NODE: undefined } });
  let stderr = ''; child.stderr?.on('data', (chunk) => { stderr += String(chunk); });
  const exit = await Promise.race([new Promise((resolve) => child.once('exit', (code, signal) => resolve({ code, signal }))), sleep(20_000).then(() => ({ timeout: true }))]);
  if (exit.timeout) {
    if (windows) { try { execFileSync('taskkill.exe', ['/PID', String(child.pid), '/T', '/F']); } catch { /* assertion below reports timeout */ } }
    else child.kill('SIGKILL');
  }
  await sleep(500);
  const processes = inventory(app);
  let diagnostic;
  try { diagnostic = JSON.parse(await readFile(path.join(userData, 'startup-error.json'), 'utf8')); } catch { diagnostic = null; }
  const serialized = JSON.stringify(diagnostic);
  try {
    if (exit.code !== 1 || processes.length || diagnostic?.code !== 'STARTUP_FAILED' || diagnostic?.stage !== expected.stage || diagnostic?.error?.message !== expected.message || serialized.includes(secret)) throw new Error(`${name} assertion failed: ${JSON.stringify({ exit, processes, diagnostic, stderr })}`);
    results.push({ name, exit, diagnostic: { code: diagnostic.code, stage: diagnostic.stage, message: diagnostic.error.message }, processesAfter: 0 });
  } finally { await rm(root, { recursive: true, force: true }); }
}

await run('corrupt-runtime', async (app) => {
  const resources = mac ? path.join(app, 'Contents/Resources') : path.join(app, 'resources');
  const manifestFile = path.join(resources, 'private-runtime/manifest.json');
  const manifest = JSON.parse(await readFile(manifestFile, 'utf8')); manifest.node.sha256 = '0'.repeat(64); await writeFile(manifestFile, JSON.stringify(manifest));
}, { stage: 'runtime-validation', message: 'Node resource hash mismatch' });

await run('missing-renderer', async (app) => {
  const resources = mac ? path.join(app, 'Contents/Resources') : path.join(app, 'resources');
  const archive = path.join(resources, 'app.asar');
  const extracted = path.join(resources, 'app');
  asar.extractAll(archive, extracted);
  await rm(path.join(extracted, 'dist/renderer/index.html'));
  await rm(archive);
}, { stage: 'renderer-load', message: 'Unexpected startup error' });

console.log(JSON.stringify(results));

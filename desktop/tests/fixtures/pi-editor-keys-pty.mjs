// Standalone bounded offline integration runner. Not part of unit fake-clock suite.
// node /absolute/path/pi-editor-keys-pty.mjs [--baseline] [--entry /absolute/pi~BUN.mjs --executable /absolute/node]
import assert from 'node:assert/strict';
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, copyFileSync, cpSync, chmodSync, rmSync, existsSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { createRequire } from 'node:module';
const here = path.dirname(fileURLToPath(import.meta.url));
const desktop = path.resolve(here, '../..');
const require = createRequire(path.join(desktop, 'package.json'));
const ptyRoot = path.dirname(require.resolve('node-pty/package.json'));
const arg = (name, fallback) => process.argv.includes(name) ? process.argv[process.argv.indexOf(name) + 1] : fallback;
const entry = arg('--entry', path.join(desktop, 'runtime/pi/node_modules/@earendil-works/pi-coding-agent/dist/cli.js'));
const executable = arg('--executable', process.execPath);
assert.ok(path.isAbsolute(entry) && path.isAbsolute(executable));
const baseline = process.argv.includes('--baseline');
const work = mkdtempSync(path.join(tmpdir(), 'pi-editor-keys-owned-'));
let child;
let exited = false;
let output = '';
try {
  for (const dir of ['home', 'agent', 'cwd']) mkdirSync(path.join(work, dir));
  // Installed symlinks stay READ-ONLY. Packaged mac spawn-helper lacks executable
  // mode here: use an owned disposable copy, never chmod installed node_modules.
  const ownedPty = path.join(work, 'pty-runtime');
  cpSync(path.join(ptyRoot, 'lib'), path.join(ownedPty, 'lib'), { recursive: true });
  const nativeDir = `prebuilds/${process.platform}-${process.arch}`;
  cpSync(path.join(ptyRoot, nativeDir), path.join(ownedPty, nativeDir), { recursive: true });
  const helper = path.join(ownedPty, nativeDir, 'spawn-helper');
  if (existsSync(helper)) chmodSync(helper, 0o700);
  const pty = require(path.join(ownedPty, 'lib/index.js'));
  const go = readFileSync(path.resolve(desktop, '../cmd/vc/pi_extension.go'), 'utf8');
  const marker = 'const piVoidCodexExtensionSource = `';
  const start = go.indexOf(marker); assert.ok(start >= 0);
  const end = go.indexOf('`', start + marker.length); assert.ok(end > start);
  writeFileSync(path.join(work, 'managed.ts'), go.slice(start + marker.length, end));
  copyFileSync(path.join(here, 'pi-editor-keys-pty-extension.ts'), path.join(work, 'probe.ts'));
  const observer = path.join(work, 'observer.jsonl');
  const env = {
    PATH: `${path.dirname(executable)}:/usr/bin:/bin`, HOME: path.join(work, 'home'), USERPROFILE: path.join(work, 'home'),
    TMPDIR: work, XDG_CONFIG_HOME: path.join(work, 'home'), XDG_CACHE_HOME: path.join(work, 'home'),
    PI_CODING_AGENT_DIR: path.join(work, 'agent'), PI_OFFLINE: '1', TERM: 'xterm-256color', LANG: 'en_US.UTF-8',
    ...(path.basename(entry) === 'pi~BUN.mjs' ? { PI_PACKAGE_DIR: path.dirname(entry) } : {}),
    VC_EDITOR_KEYS_OWNED_PTY: '1', VC_EDITOR_KEYS_OBSERVER: observer, VC_BOOTSTRAP_EXECUTABLE: executable,
    ...(baseline ? { VC_EDITOR_KEYS_BASELINE: '1' } : {}),
  };
  child = pty.spawn(executable, [entry, '--no-session', '--no-extensions', '--no-skills', '--no-prompt-templates', '--no-themes', '--no-builtin-tools', '--provider', 'editor-keys-offline', '--model', 'fixture', '--tui-mode', 'fullscreen', '-e', path.join(work, 'probe.ts')], { cwd: path.join(work, 'cwd'), env, cols: 100, rows: 30, name: 'xterm-256color' });
  child.onData(data => { output = (output + data).slice(-12000); });
  child.onExit(() => { exited = true; });
  const records = () => existsSync(observer) ? readFileSync(observer, 'utf8').split('\n').filter(Boolean).map(line => JSON.parse(line)) : [];
  const wait = async (predicate, label) => {
    const deadline = Date.now() + 12000;
    while (Date.now() < deadline && !exited) {
      const rows = records(); assert.ok(!rows.some(row => row.event.startsWith('forbidden-')), 'network/inference forbidden');
      const match = predicate(rows); if (match) return match;
      await new Promise(resolve => setTimeout(resolve, 20));
    }
    // Owned synthetic terminal only; no inherited credentials or real prompts.
    throw new Error(`${label}: ${exited ? 'child exited' : 'bounded wait expired'}; owned terminal tail=${JSON.stringify(output.slice(-2500))}`);
  };
  await wait(rows => rows.find(row => row.event === 'ready'), 'actual interactive editor ready');
  const send = async data => {
    const before = records().filter(row => row.event === 'snapshot').length;
    child.write(data);
    return wait(rows => rows.filter(row => row.event === 'snapshot').length > before && rows.filter(row => row.event === 'snapshot').at(-1), 'reader processed key');
  };
  await send('\x1b[200~owned 😀\nmiddle 世界\nlast\x1b[201~');
  const escaped = await send('\x1b');
  assert.equal(baseline ? escaped.draft : escaped.empty, true, baseline ? 'native Esc control preserves draft' : 'K7 RED: default managed Esc must clear actual interactive editor');
  if (!baseline) {
    const undone = await send('\x1f'); assert.equal(undone.draft, true, 'native undo');
    await send('\x1b');
    for (const [index, prompt] of ['owned USER one', 'owned USER two'].entries()) {
      await send(prompt); await send('\r');
      await wait(rows => rows.find(row => row.event === 'handled' && row.count === index + 1), 'offline handled USER prompt');
    }
    await send('\x1b[200~owned 😀\nmiddle 世界\nlast\x1b[201~');
    await send('\x01'); const middle = await send('\x02');
    assert.equal(middle.cursor.line, 1);
    assert.equal((await send('\x1b[A')).latest, true);
    assert.equal((await send('\x1b[A')).older, true);
    assert.equal((await send('\x1b[B')).latest, true);
    const restored = await send('\x1b[B'); assert.equal(restored.draft, true); assert.deepEqual(restored.cursor, middle.cursor);
  }
  console.log(JSON.stringify({ event: baseline ? 'NATIVE_PTY_CONTROL_OK' : 'MANAGED_PTY_KEYS_OK', entry, inference: 0, guiProof: false }));
} finally {
  if (child && !exited) {
    child.kill('SIGTERM');
    const deadline = Date.now() + 2000;
    while (!exited && Date.now() < deadline) await new Promise(resolve => setTimeout(resolve, 20));
    if (!exited) {
      child.kill('SIGKILL');
      const killDeadline = Date.now() + 2000;
      while (!exited && Date.now() < killDeadline) await new Promise(resolve => setTimeout(resolve, 20));
      assert.ok(exited, 'owned PTY child must be reaped');
    }
  }
  rmSync(work, { recursive: true, force: true });
}

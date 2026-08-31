import { execFile, execFileSync, spawnSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { createRequire } from 'node:module';
import { chmodSync } from 'node:fs';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { clearTimeout } from 'node:timers';

const require = createRequire(import.meta.url);
const packageJson = JSON.parse(await readFile(path.resolve('package.json'), 'utf8'));
const electronVersion = packageJson.devDependencies?.electron;
const installedElectronVersion = require('electron/package.json').version;
if (typeof electronVersion !== 'string' || !/^\d+\.\d+\.\d+$/.test(electronVersion) || installedElectronVersion !== electronVersion) throw new Error('Electron package pin mismatch');
chmodSync(path.resolve('node_modules/node-pty/prebuilds/darwin-arm64/spawn-helper'), 0o755);
const pty = require('node-pty');
const { wrapPty } = require('../dist/main/session-manager.js');
const app = path.resolve('release/mac-arm64/Void Code.app');
const resources = path.join(app, 'Contents/Resources');
const runtime = path.join(resources, 'private-runtime');
const manifest = JSON.parse(await readFile(path.join(runtime, 'manifest.json'), 'utf8'));
const sha = async (file) => createHash('sha256').update(await readFile(file)).digest('hex');
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const alive = (pid) => { try { process.kill(pid, 0); return true; } catch { return false; } };
const waitGone = async (pids) => {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    if (pids.every((pid) => !alive(pid))) return;
    await sleep(50);
  }
  throw new Error(`owned process survived cleanup: ${pids.filter(alive).join(',')}`);
};
const inventory = () => execFileSync('/bin/ps', ['-axo', 'pid=,ppid=,pgid=,command='], { encoding: 'utf8' }).split('\n').filter((line) => line.includes(`${app}/Contents/`) || line.includes(`${runtime}/`));

if (inventory().length) throw new Error(`stale app-owned process inventory count=${inventory().length}`);
const asarList = execFileSync(path.resolve('node_modules/.bin/asar'), ['list', path.join(resources, 'app.asar')], { encoding: 'utf8' });
if (!asarList.includes('/dist/main/session-manager.js') || !asarList.includes('/dist/renderer/index.js') || !asarList.includes('/dist/renderer/index.css') || !/\/dist\/renderer\/assets\/jetbrains-mono-[^/]+\.woff2/.test(asarList)) throw new Error('packaged consumer seam incomplete');
if (asarList.includes('/private-runtime/')) throw new Error('private runtime unexpectedly entered asar');
const native = path.join(resources, 'app.asar.unpacked/node_modules/node-pty/prebuilds/darwin-arm64/pty.node');
const privateNpm = path.join(runtime, 'node/bin/npm');
if (!manifest.node.npm?.version || !manifest.node.npm?.treeSha256) throw new Error('private npm manifest facts missing');
const npmVersion = execFileSync(privateNpm, ['--version'], { encoding: 'utf8', env: { PATH: `${path.dirname(privateNpm)}:/usr/bin:/bin` } }).trim();
if (npmVersion !== manifest.node.npm.version) throw new Error('the private npm reports a different version than the manifest');
const signature = spawnSync('/usr/bin/codesign', ['--verify', '--deep', '--strict', app], { encoding: 'utf8' });
if (signature.status === 0 || !signature.stderr.includes('signature indicates they must be present')) throw new Error('unexpected local signature disposition');

const baseline = execFile('/bin/sleep', ['30']);
const baselinePid = baseline.pid;
if (!baselinePid) throw new Error('baseline process did not start');
const lifecycle = [];
try {
  for (const mode of ['close', 'forced-root-failure']) {
    const source = `const{spawn}=require('node:child_process');const c=spawn(process.execPath,['-e','setInterval(()=>{},1000)'],{stdio:'inherit'});console.log('PIDS:'+process.pid+':'+c.pid);${mode === 'forced-root-failure' ? "setTimeout(()=>process.kill(process.pid,'SIGKILL'),150);" : ''}setInterval(()=>{},1000);`;
    const terminal = wrapPty(pty.spawn(manifest.node.path ? path.join(runtime, manifest.node.path) : path.join(runtime, 'node/bin/node'), ['-e', source], {
      name: 'xterm-256color', cols: 80, rows: 24, cwd: runtime, env: { PATH: '/usr/bin:/bin' },
    }));
    let processIds;
    const exited = new Promise((resolve) => terminal.onExit(resolve));
    await new Promise((resolve, reject) => {
      const timeout = setTimeout(() => reject(new Error(`${mode} PTY did not report its process tree`)), 5000);
      terminal.onData((data) => { const match = /PIDS:(\d+):(\d+)/.exec(data); if (match) { clearTimeout(timeout); processIds = [Number(match[1]), Number(match[2])]; resolve(); } });
    });
    if (mode === 'close') terminal.kill();
    await Promise.race([exited, sleep(5000).then(() => { throw new Error(`${mode} PTY did not exit`); })]);
    await waitGone(processIds);
    if (!alive(baselinePid)) throw new Error(`unrelated baseline changed during ${mode}`);
    lifecycle.push({ mode, ownedPidsAfter: 0, unrelatedBaselinePreserved: true });
  }
} finally {
  baseline.kill('SIGTERM');
}
await sleep(100);
const after = inventory();
if (after.length) throw new Error(`stale app-owned processes after lifecycle probes: ${after.join(';')}`);

const result = {
  package: { electron: electronVersion, xterm: '6.0.0', nodePty: '1.1.0' },
  runtime: { vc: manifest.vc.version, vcSourceCommit: manifest.vc.sourceCommit, node: manifest.node.version, npm: manifest.node.npm.version, pi: manifest.pi.version },
  hashes: { appAsar: await sha(path.join(resources, 'app.asar')), nativePty: await sha(native), vc: manifest.vc.sha256, node: manifest.node.sha256, piTree: manifest.pi.treeSha256 },
  boundaries: { nativeModuleOutsideAsar: true, privateRuntimeOutsideAsar: true, signing: 'expected-unsigned-local-prototype (identity=null)', staleOwnedProcessCount: after.length },
  lifecycle,
};
console.log(JSON.stringify(result));

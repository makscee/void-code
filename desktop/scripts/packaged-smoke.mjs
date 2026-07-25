import { execFile, execFileSync } from 'node:child_process';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';

if (process.platform !== 'darwin' || process.arch !== 'arm64') throw new Error('packaged smoke supports only macOS-arm64');
const appBinary = path.resolve('release/mac-arm64/Void Code Prototype.app/Contents/MacOS/Void Code Prototype');
const resources = path.resolve('release/mac-arm64/Void Code Prototype.app/Contents/Resources/private-runtime');
const temporary = await mkdtemp(path.join(os.tmpdir(), 'vc-desktop-smoke-'));
const output = path.join(temporary, 'result.json');
const inventory = () => execFileSync('/bin/ps', ['-axo', 'pid=,command='], { encoding: 'utf8' }).split('\n').filter((line) => line.includes('private-runtime/fixture/round-trip.js') || line.includes('node-pty/prebuilds/darwin-arm64/spawn-helper') || line.includes(appBinary));
const globalLookupAbsent = ['node', 'pi'].every((name) => {
  try { execFileSync('/usr/bin/which', [name], { env: { PATH: '/usr/bin:/bin' }, stdio: 'ignore' }); return false; } catch { return true; }
});
if (!globalLookupAbsent) throw new Error('global Node or Pi unexpectedly resolved on restricted PATH');
const before = inventory();
if (before.length) throw new Error(`dirty fixture inventory before smoke: ${before.join('; ')}`);
const child = execFile(appBinary, [`--void-smoke-output=${output}`], {
  env: { HOME: process.env.HOME, TMPDIR: process.env.TMPDIR ?? '/tmp', PATH: '/usr/bin:/bin', ELECTRON_RUN_AS_NODE: undefined },
});
let stderr = '';
child.stderr?.on('data', (chunk) => { stderr += String(chunk); });
const exit = await Promise.race([
  new Promise((resolve) => child.on('exit', (code, signal) => resolve({ code, signal }))),
  new Promise((_, reject) => setTimeout(() => { child.kill('SIGKILL'); reject(new Error('packaged smoke timed out')); }, 20_000)),
]);
await new Promise((resolve) => setTimeout(resolve, 500));
const result = JSON.parse(await readFile(output, 'utf8'));
const after = inventory();
const manifest = JSON.parse(await readFile(path.join(resources, 'manifest.json'), 'utf8'));
const expectedApi = ['chooseFolder', 'input', 'onExit', 'onOutput', 'openLink', 'resize', 'start', 'status', 'stop', 'teardown'];
if (exit.code !== 0 || !result.ok || result.rendererAuthority?.process !== 'undefined' || result.rendererAuthority?.require !== 'undefined' || JSON.stringify(result.apiKeys) !== JSON.stringify(expectedApi) || after.length) throw new Error(JSON.stringify({ exit, result, before, after, stderr }));
console.log(JSON.stringify({ exit, result, processInventory: { before, after }, environment: { PATH: '/usr/bin:/bin', globalNodePiAbsent: globalLookupAbsent, electronRunAsNode: false }, manifest }));
await rm(temporary, { recursive: true, force: true });

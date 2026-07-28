import { execFile, execFileSync } from 'node:child_process';
import { mkdtemp, rm, writeFile } from 'node:fs/promises';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';

const mac = process.platform === 'darwin' && process.arch === 'arm64';
const windows = process.platform === 'win32' && process.arch === 'x64';
if (!mac && !windows) throw new Error('packaged window check supports macOS-arm64 and Windows-x64');
const defaultApp = mac ? 'release/mac-arm64/Void Code.app' : 'release/win-unpacked';
const app = process.env.VOID_PACKAGED_APP ? path.resolve(process.env.VOID_PACKAGED_APP) : path.resolve(defaultApp);
const binary = mac ? path.join(app, 'Contents/MacOS/Void Code') : path.join(app, 'Void Code.exe');
const powershell = windows ? path.join(process.env.SystemRoot ?? 'C:\\Windows', 'System32/WindowsPowerShell/v1.0/powershell.exe') : undefined;
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const port = await new Promise((resolve, reject) => { const server = net.createServer(); server.once('error', reject); server.listen(0, '127.0.0.1', () => { const address = server.address(); server.close(() => resolve(address.port)); }); });
const root = await mkdtemp(path.join(os.tmpdir(), 'void-window-check-'));
const args = [`--user-data-dir=${path.join(root, 'user-data')}`, `--remote-debugging-port=${port}`];
function inventory() {
  if (mac) return execFileSync('/bin/ps', ['-axo', 'pid=,ppid=,command='], { encoding: 'utf8' }).split('\n').filter((line) => line.includes(app)).map((line) => {
    const match = /^\s*(\d+)\s+(\d+)\s+(.*)$/.exec(line); return { pid: Number(match?.[1]), ppid: Number(match?.[2]), command: match?.[3] ?? '' };
  });
  const literal = app.replaceAll("'", "''");
  const output = execFileSync(powershell, ['-NoProfile', '-NonInteractive', '-Command', `@(Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -like '${literal}*' } | Select-Object @{n='pid';e={$_.ProcessId}},@{n='ppid';e={$_.ParentProcessId}},@{n='command';e={$_.CommandLine}}) | ConvertTo-Json -Compress`], { encoding: 'utf8' }).trim();
  if (!output) return []; const parsed = JSON.parse(output); return Array.isArray(parsed) ? parsed : [parsed];
}
const roots = () => inventory().filter((process) => process.command.startsWith(mac ? binary : `"${binary}"`) && !process.command.includes('--type='));
const launch = () => execFile(binary, args, { env: { HOME: process.env.HOME, USERPROFILE: process.env.USERPROFILE, TEMP: process.env.TEMP, TMPDIR: process.env.TMPDIR ?? '/tmp', PATH: mac ? '/usr/bin:/bin' : `${process.env.SystemRoot}\\System32`, ELECTRON_RUN_AS_NODE: undefined } });
const waitExit = (child, timeout) => Promise.race([new Promise((resolve) => child.once('exit', (code, signal) => resolve({ code, signal }))), sleep(timeout).then(() => ({ timeout: true }))]);
async function inspectNativeWindow(pid, temporary) {
  if (windows) {
    const script = `Add-Type @'\nusing System; using System.Runtime.InteropServices; public class VCWindow { [StructLayout(LayoutKind.Sequential)] public struct RECT { public int Left,Top,Right,Bottom; } [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h); [DllImport("user32.dll")] public static extern bool IsIconic(IntPtr h); [DllImport("user32.dll")] public static extern IntPtr GetForegroundWindow(); [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h,out RECT r); }\n'@; $p=Get-Process -Id ${pid}; $h=$p.MainWindowHandle; $r=New-Object VCWindow+RECT; $ok=($h -ne 0 -and [VCWindow]::IsWindowVisible($h) -and [VCWindow]::GetWindowRect($h,[ref]$r)); @{visible=$ok;minimized=if($h -ne 0){[VCWindow]::IsIconic($h)}else{$false};foreground=($h -ne 0 -and [VCWindow]::GetForegroundWindow() -eq $h);width=if($ok){$r.Right-$r.Left}else{0};height=if($ok){$r.Bottom-$r.Top}else{0}} | ConvertTo-Json -Compress`;
    return JSON.parse(execFileSync(powershell, ['-NoProfile', '-NonInteractive', '-Command', script], { encoding: 'utf8' }));
  }
  const source = path.join(temporary, 'window-census.swift');
  await writeFile(source, `import Foundation\nimport CoreGraphics\nlet pid = Int32(CommandLine.arguments[1])!\nlet windows = CGWindowListCopyWindowInfo([.optionOnScreenOnly, .excludeDesktopElements], kCGNullWindowID) as! [[String: Any]]\nfor window in windows {\n  guard (window[kCGWindowOwnerPID as String] as? NSNumber)?.int32Value == pid else { continue }\n  guard (window[kCGWindowLayer as String] as? NSNumber)?.intValue == 0 else { continue }\n  guard let raw = window[kCGWindowBounds as String], let bounds = CGRect(dictionaryRepresentation: raw as! CFDictionary) else { continue }\n  let alpha = (window[kCGWindowAlpha as String] as? NSNumber)?.doubleValue ?? 0\n  let result: [String: Any] = ["visible": alpha > 0, "width": bounds.width, "height": bounds.height]\n  print(String(data: try! JSONSerialization.data(withJSONObject: result), encoding: .utf8)!)\n  exit(0)\n}\nprint("{\\"visible\\":false,\\"width\\":0,\\"height\\":0}")\n`);
  return JSON.parse(execFileSync('/usr/bin/swift', [source, String(pid)], { encoding: 'utf8' }));
}
function minimizeNativeWindow(pid) {
  if (!windows) return;
  const script = `Add-Type @'\nusing System; using System.Runtime.InteropServices; public class VCMinimize { [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h,int command); }\n'@; $h=(Get-Process -Id ${pid}).MainWindowHandle; if($h -eq 0 -or -not [VCMinimize]::ShowWindow($h,6)){exit 1}`;
  execFileSync(powershell, ['-NoProfile', '-NonInteractive', '-Command', script]);
}
async function target() {
  for (let attempt = 0; attempt < 300; attempt += 1) {
    try { const targets = await (await globalThis.fetch(`http://127.0.0.1:${port}/json/list`)).json(); const page = targets.find((entry) => entry.type === 'page' && entry.url.includes('/renderer/index.html')); if (page) return page; } catch { /* startup not listening yet */ }
    await sleep(100);
  }
  throw new Error(`normal renderer target absent: ${JSON.stringify(inventory())}`);
}
async function evaluate(targetInfo, expression) {
  const socket = new globalThis.WebSocket(targetInfo.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.onopen = resolve; socket.onerror = reject; });
  const reply = new Promise((resolve, reject) => { socket.onmessage = (event) => { const value = JSON.parse(String(event.data)); if (value.id === 1) resolve(value); }; socket.onerror = reject; });
  socket.send(JSON.stringify({ id: 1, method: 'Runtime.evaluate', params: { expression, returnByValue: true } }));
  const result = await reply; socket.close(); return result.result.result.value;
}
let primary;
let secondary;
try {
  primary = launch();
  const page = await target();
  let renderer;
  for (let attempt = 0; attempt < 100; attempt += 1) {
    renderer = await evaluate(page, '({ title: document.title, visibility: document.visibilityState, href: location.href })');
    if (renderer.title) break;
    await sleep(100);
  }
  const processes = inventory();
  const native = await inspectNativeWindow(primary.pid, root);
  if (renderer.title !== 'Void Code' || renderer.visibility !== 'visible' || !renderer.href.endsWith('/renderer/index.html') || !processes.some((process) => process.command.includes('--type=renderer')) || !native.visible || native.width < 500 || native.height < 500) throw new Error(`normal window assertion failed: ${JSON.stringify({ renderer, native, processes })}`);
  minimizeNativeWindow(primary.pid);
  if (windows) {
    const minimized = await inspectNativeWindow(primary.pid, root);
    if (!minimized.minimized) throw new Error(`window did not minimize before second-instance focus check: ${JSON.stringify(minimized)}`);
  }
  secondary = launch();
  const secondExit = await waitExit(secondary, 10_000);
  await sleep(500);
  const rootProcesses = roots();
  const focused = windows ? await inspectNativeWindow(primary.pid, root) : undefined;
  if (secondExit.code !== 0 || rootProcesses.length !== 1 || (focused && (focused.minimized || !focused.foreground))) throw new Error(`single-instance assertion failed: ${JSON.stringify({ secondExit, rootProcesses, focused, processes: inventory() })}`);
  console.log(JSON.stringify({ renderer, nativeWindow: native, secondExit, roots: rootProcesses, secondInstanceFocus: focused, rendererProcess: true }));
} finally {
  if (secondary && secondary.exitCode === null) secondary.kill('SIGKILL');
  if (primary && primary.exitCode === null) primary.kill(mac ? 'SIGTERM' : undefined);
  await sleep(500);
  for (const item of inventory()) { try { process.kill(item.pid, 'SIGKILL'); } catch { /* already gone */ } }
  await rm(root, { recursive: true, force: true });
}

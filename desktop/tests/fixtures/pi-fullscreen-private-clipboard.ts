import { spawnSync, type SpawnSyncReturns } from 'node:child_process';
import path from 'node:path';

export type NativeRequest = {
  work: string;
  node: string;
  args: string[];
  env: NodeJS.ProcessEnv;
};

// Test-only. Compile before launching any Pi/native code; the compiler never touches clipboard.
// Dependency injection is only for fake unit controls, never selected via environment flags.
export function runPrivateWindowsClipboard(request: NativeRequest, spawn = spawnSync): SpawnSyncReturns<string> {
  if (request.env.VC_ISOLATED_CLIPBOARD_ACCEPTANCE !== 'I_OWN_THIS_ISOLATED_CLIPBOARD_SESSION') {
    throw new Error('PRIVATE_CLIPBOARD_GATE_REQUIRED');
  }
  const root = request.env.SystemRoot;
  if (!root || !path.isAbsolute(root) || !path.isAbsolute(request.node) || !path.isAbsolute(request.args[0] ?? '')) {
    throw new Error('PRIVATE_CLIPBOARD_INPUT_REQUIRED');
  }
  const executable = path.join(request.work, 'private-clipboard.exe');
  const source = path.resolve('tests/fixtures/pi-fullscreen-private-clipboard.cs');
  const options = {
    cwd: request.work, env: request.env, encoding: 'utf8' as const,
    maxBuffer: 2 * 1024 * 1024, stdio: ['ignore', 'pipe', 'pipe'] as ['ignore', 'pipe', 'pipe'],
    windowsHide: true,
  };
  // Invoke the inbox .NET Framework compiler directly: no PowerShell/CodeDOM compiler
  // grandchild can outlive a pre-run timeout. Windows x64 + .NET Framework are required.
  const compile = spawn(path.join(root, 'Microsoft.NET', 'Framework64', 'v4.0.30319', 'csc.exe'), [
    '/nologo', '/target:exe', '/platform:x64', `/out:${executable}`, source,
  ], { ...options, timeout: 30000 });
  // Compiler errors can contain paths/source. Return only fixed, privacy-safe values.
  if (compile.error || compile.status !== 0) throw new Error('PRIVATE_CLIPBOARD_COMPILE_FAILED');
  // Lifetime job kills owned descendants if this synchronous outer deadline kills the launcher.
  // Launcher itself has 45s child / 55s worker bounds and reaps its child job before emitting output.
  return spawn(executable, [request.node, ...request.args], { ...options, timeout: 60000 });
}

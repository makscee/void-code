import { statSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import type { IPty } from 'node-pty';
import type { RealStartRequest, StartRequest } from '../shared/contract';
import type { PrivateRuntime } from './resources';
import type { StatusWriteAuthority } from './status-channel';
import { desktopChildEnv, fixtureChildEnv } from './desktop-child-env';
import { sessionLifecycleArgs } from './session-files';

interface SpawnOptions { name: string; cols: number; rows: number; cwd: string; useConptyDll?: boolean; env: Record<string, string>; }
export type PtySpawner = (file: string, args: string[], options: SpawnOptions) => IPty;

export function spawnDesktopRequest(runtime: PrivateRuntime, request: StartRequest, spawn: PtySpawner, authority?: StatusWriteAuthority): IPty {
  const conpty = process.platform === 'win32' ? { useConptyDll: true } : {};
  if ('fixture' in request) return spawn(runtime.node, [runtime.fixture], {
    name: 'xterm-256color', cols: 80, rows: 24, cwd: runtime.root, ...conpty,
    // Built next door rather than here. Written out at this call site it drifted from the session
    // environment for four weeks, and the variable it was missing is the one Node cannot start
    // without on Windows.
    env: fixtureChildEnv(process.platform === 'win32' ? 'win32' : 'darwin', process.env),
  });
  const real = request as RealStartRequest;
  if (!statSync(real.cwd).isDirectory()) throw new Error('selected folder is unavailable');
  const lifecycle = sessionLifecycleArgs(path.join(os.homedir(), '.pi/agent/sessions'), real.sessionId, real.mode, real.cwd);
  return spawn(runtime.vc, ['desktop-session', '--node', runtime.node, '--pi-entry', runtime.piEntry, '--', ...lifecycle], {
    name: 'xterm-256color', cols: 100, rows: 30, cwd: real.cwd, ...conpty,
    env: desktopChildEnv(process.platform === 'win32' ? 'win32' : 'darwin', process.env, runtime.node, authority, runtime.piPackageDir),
  });
}

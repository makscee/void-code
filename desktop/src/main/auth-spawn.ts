import { spawn } from 'node:child_process';
import type { AuthChildProcess, AuthSpawner } from './auth-session';

// The one seam standing between "signed in through the vc binary this app shipped" and
// "signed in through whatever vc happens to resolve first on PATH" — a different, unverified
// build. spawn() gets the exact path handed to it, with no shell involved to reopen that lookup.
export const spawnAuthProcess: AuthSpawner = (vcPath, args) =>
  spawn(vcPath, args, { shell: false, stdio: ['ignore', 'pipe', 'pipe'] }) as unknown as AuthChildProcess;

import { spawn } from 'node:child_process';
import type { AuthChildProcess, AuthSpawner } from './auth-session';
import { resolveAccessCheckHost } from './access-check-host';

// The one seam standing between "signed in through the vc binary this app shipped" and
// "signed in through whatever vc happens to resolve first on PATH" — a different, unverified
// build. spawn() gets the exact path handed to it, with no shell involved to reopen that lookup.
//
// The child gets one variable added: the host that answers the access check. VC_AUTH_HOST is left
// exactly as the parent had it — sign-in and the Pi bootstrap have no route on the access check's
// host, and the CLI's own default is what every hand-run `vc` already depends on. Nothing is
// decided from argv: the same question gets the same answer whoever asks it.
//
// The parent environment is honoured here, unlike in desktop-child-env.ts, and the asymmetry is
// deliberate. This lane is the operator pointing the app at a stand, and a hand-run `vc` reads the
// environment anyway, so no trust boundary is crossed. The chat session's environment is built
// from an allowlist precisely so a hostile parent variable cannot choose where a bearer token
// gets sent, and that constant stays a constant.
//
// `env:` replaces the child environment wholesale rather than merging, so the parent environment
// is spread in first: handing over the one variable alone would start vc with no HOME (no
// ~/.void-code token to read) and no PATH.
export const spawnAuthProcess: AuthSpawner = (vcPath, args) =>
  spawn(vcPath, args, {
    shell: false,
    stdio: ['ignore', 'pipe', 'pipe'],
    env: { ...process.env, VC_ACCESS_CHECK_HOST: resolveAccessCheckHost(process.env) },
  }) as unknown as AuthChildProcess;

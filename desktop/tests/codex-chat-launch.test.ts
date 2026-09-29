import { mkdirSync, symlinkSync, writeFileSync } from 'node:fs';
import { mkdtemp, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { PrivateRuntime } from '../src/main/resources';
import { desktopChildEnv } from '../src/main/desktop-child-env';
import { codexLifecycleArgs, findCodexRollout, SessionDiscoveryError } from '../src/main/session-files';
import { spawnDesktopRequest } from '../src/main/spawn-request';

// Spec 2026-09-29-desktop-codex-chats-design, items 8–10: how a Codex chat is
// started and reopened.
//
// Contract pinned here:
//   findCodexRollout(sessionsRoot, codexSessionId): string | undefined
//     the file rollout-*-<id>.jsonl anywhere under sessionsRoot (Codex writes
//     sessions/YYYY/MM/DD/rollout-<timestamp>-<id>.jsonl), or undefined;
//   codexLifecycleArgs(sessionsRoot, mode, codexSessionId?): string[]
//     create                       → []
//     resume, no codexSessionId    → []   (closed before the first message: a new Codex session)
//     resume, rollout found        → ['--codex-session', id]
//     resume, rollout missing      → throws SessionDiscoveryError('SESSION_MISSING'), the Pi screen
//   spawnDesktopRequest(runtime, { sessionId, cwd, mode, runtime: 'codex', codexSessionId? }, spawn)
//     spawns runtime.vc with ['desktop-session', '--runtime', 'codex', ...lifecycle, '--'],
//     no --node/--pi-entry, the same environment a Pi chat gets, cwd = the chat folder;
//     sessionsRoot = <home>/.void-code/codex/sessions (CODEX_HOME of vc).
//   A request without runtime (or runtime 'pi') is the Pi launch, unchanged.

const CHAT = '123e4567-e89b-42d3-a456-426614174000';
const CODEX_SESSION = '01a0ec63-35c1-7d02-bc6a-c06464a61565';
const OTHER_SESSION = '01a0ec70-0000-7d02-bc6a-c06464a61565';

const roots: string[] = [];
afterEach(async () => { vi.unstubAllEnvs(); for (const root of roots.splice(0)) await rm(root, { recursive: true, force: true }); });

async function sandbox() {
  const home = await mkdtemp(path.join(os.tmpdir(), 'vc-codex-launch-')); roots.push(home);
  vi.stubEnv('HOME', home); vi.stubEnv('USERPROFILE', home);
  const cwd = path.join(home, 'work'); mkdirSync(cwd);
  const sessions = path.join(home, '.void-code', 'codex', 'sessions');
  const runtime = { root: home, node: '/private/node', fixture: '/private/fixture', vc: '/private/vc', piEntry: '/private/pi', piPackageDir: '/private/pi-package' } as PrivateRuntime;
  return { home, cwd, sessions, runtime };
}

function plantRollout(sessions: string, id: string, day = '2026/09/29', stamp = '2026-09-29T12-58-39'): string {
  const dir = path.join(sessions, ...day.split('/')); mkdirSync(dir, { recursive: true });
  const file = path.join(dir, `rollout-${stamp}-${id}.jsonl`);
  writeFileSync(file, '{"type":"session_meta"}\n');
  return file;
}

describe('finding a Codex conversation on disk', () => {
  it('finds the rollout of the session wherever Codex dated it', async () => {
    const { sessions } = await sandbox();
    const file = plantRollout(sessions, CODEX_SESSION, '2026/08/31', '2026-08-31T23-59-59');
    plantRollout(sessions, OTHER_SESSION);
    expect(findCodexRollout(sessions, CODEX_SESSION)).toBe(file);
  });

  it('finds nothing for a session with no rollout, or no sessions folder at all', async () => {
    const { home, sessions } = await sandbox();
    expect(findCodexRollout(sessions, CODEX_SESSION)).toBeUndefined();
    plantRollout(sessions, OTHER_SESSION);
    expect(findCodexRollout(sessions, CODEX_SESSION)).toBeUndefined();
    expect(findCodexRollout(path.join(home, 'nowhere'), CODEX_SESSION)).toBeUndefined();
  });

  it('matches the whole name, not a fragment of it', async () => {
    const { sessions } = await sandbox();
    const dir = path.join(sessions, '2026', '09', '29'); mkdirSync(dir, { recursive: true });
    for (const name of [`rollout-2026-09-29T12-58-39-${CODEX_SESSION}.jsonl.tmp`, `rollout-2026-09-29T12-58-39-x${CODEX_SESSION}.jsonl`, `backup-${CODEX_SESSION}.jsonl`, `${CODEX_SESSION}.jsonl`]) {
      writeFileSync(path.join(dir, name), '{}\n');
    }
    mkdirSync(path.join(dir, `rollout-2026-09-29T12-58-39-${CODEX_SESSION}.jsonl-dir`));
    expect(findCodexRollout(sessions, CODEX_SESSION)).toBeUndefined();
  });

  it('does not follow a link out of the sessions folder', async () => {
    const { home, sessions } = await sandbox();
    const outside = path.join(home, 'elsewhere'); plantRollout(outside, CODEX_SESSION);
    mkdirSync(sessions, { recursive: true });
    symlinkSync(outside, path.join(sessions, 'linked'));
    expect(findCodexRollout(sessions, CODEX_SESSION)).toBeUndefined();
  });

  it('refuses an id that is not a UUID instead of globbing with it', async () => {
    const { sessions } = await sandbox();
    plantRollout(sessions, CODEX_SESSION);
    expect(() => findCodexRollout(sessions, '*')).toThrow();
  });
});

describe('what a Codex chat is started with', () => {
  it('starts a new chat with no lifecycle arguments', async () => {
    const { sessions } = await sandbox();
    expect(codexLifecycleArgs(sessions, 'create', undefined)).toEqual([]);
  });

  it('reopens the saved conversation when its rollout exists', async () => {
    const { sessions } = await sandbox(); plantRollout(sessions, CODEX_SESSION);
    expect(codexLifecycleArgs(sessions, 'resume', CODEX_SESSION)).toEqual(['--codex-session', CODEX_SESSION]);
  });

  it('starts a new Codex session for a chat closed before its first message', async () => {
    const { sessions } = await sandbox();
    expect(codexLifecycleArgs(sessions, 'resume', undefined)).toEqual([]);
  });

  it('shows the Pi SESSION_MISSING screen when the saved conversation is gone', async () => {
    const { sessions } = await sandbox(); plantRollout(sessions, OTHER_SESSION);
    let caught: unknown;
    try { codexLifecycleArgs(sessions, 'resume', CODEX_SESSION); } catch (error) { caught = error; }
    expect(caught).toBeInstanceOf(SessionDiscoveryError);
    expect((caught as SessionDiscoveryError).code).toBe('SESSION_MISSING');
    expect(String((caught as Error).message)).toContain('SESSION_MISSING');
  });
});

describe('spawning a Codex chat', () => {
  it('runs vc desktop-session --runtime codex for a new chat, in the chat folder', async () => {
    const { cwd, runtime } = await sandbox();
    const spawn = vi.fn();
    spawnDesktopRequest(runtime, { sessionId: CHAT, cwd, mode: 'create', runtime: 'codex' } as never, spawn);
    expect(spawn).toHaveBeenCalledOnce();
    const [file, args, options] = spawn.mock.calls[0];
    expect(file).toBe('/private/vc');
    expect(args).toEqual(['desktop-session', '--runtime', 'codex', '--']);
    expect(options.cwd).toBe(cwd);
  });

  it('reopens the saved conversation with --codex-session', async () => {
    const { cwd, sessions, runtime } = await sandbox(); plantRollout(sessions, CODEX_SESSION);
    const spawn = vi.fn();
    spawnDesktopRequest(runtime, { sessionId: CHAT, cwd, mode: 'resume', runtime: 'codex', codexSessionId: CODEX_SESSION } as never, spawn);
    expect(spawn.mock.calls[0][1]).toEqual(['desktop-session', '--runtime', 'codex', '--codex-session', CODEX_SESSION, '--']);
  });

  it('starts fresh for a Codex chat that never got a session', async () => {
    const { cwd, runtime } = await sandbox();
    const spawn = vi.fn();
    spawnDesktopRequest(runtime, { sessionId: CHAT, cwd, mode: 'resume', runtime: 'codex' } as never, spawn);
    expect(spawn.mock.calls[0][1]).toEqual(['desktop-session', '--runtime', 'codex', '--']);
  });

  it('does not spawn when the saved conversation is missing', async () => {
    const { cwd, runtime } = await sandbox();
    const spawn = vi.fn();
    expect(() => spawnDesktopRequest(runtime, { sessionId: CHAT, cwd, mode: 'resume', runtime: 'codex', codexSessionId: CODEX_SESSION } as never, spawn)).toThrow('SESSION_MISSING');
    expect(spawn).not.toHaveBeenCalled();
  });

  it('never looks for a Pi session file for a Codex chat', async () => {
    // A Codex chat has no Pi session; the Pi lookup would call it missing.
    const { cwd, sessions, runtime } = await sandbox(); plantRollout(sessions, CODEX_SESSION);
    const spawn = vi.fn();
    expect(() => spawnDesktopRequest(runtime, { sessionId: CHAT, cwd, mode: 'resume', runtime: 'codex', codexSessionId: CODEX_SESSION } as never, spawn)).not.toThrow();
    expect(spawn).toHaveBeenCalledOnce();
  });

  it('gives Codex the same environment a Pi chat gets, status channel included', async () => {
    const { cwd, runtime } = await sandbox();
    const authority = { path: path.join(cwd, 'status.json'), chatId: CHAT, generation: 5 };
    const spawn = vi.fn();
    spawnDesktopRequest(runtime, { sessionId: CHAT, cwd, mode: 'create', runtime: 'codex' } as never, spawn, authority, 'darwin');
    const env = spawn.mock.calls[0][2].env as Record<string, string>;
    expect(env).toEqual(desktopChildEnv('darwin', process.env, runtime.node, authority, runtime.piPackageDir));
    expect(env.VC_DESKTOP_STATUS_PATH).toBe(authority.path);
    expect(env.VC_DESKTOP_CHAT_ID).toBe(CHAT);
    expect(env.VC_DESKTOP_STATUS_GENERATION).toBe('5');
  });

  it('launches a Pi chat exactly as before', async () => {
    const { cwd, runtime } = await sandbox();
    for (const request of [{ sessionId: CHAT, cwd, mode: 'create' }, { sessionId: CHAT, cwd, mode: 'create', runtime: 'pi' }]) {
      const spawn = vi.fn();
      spawnDesktopRequest(runtime, request as never, spawn);
      expect(spawn.mock.calls[0][1]).toEqual(['desktop-session', '--node', '/private/node', '--pi-entry', '/private/pi', '--', '--session-id', CHAT]);
    }
  });
});

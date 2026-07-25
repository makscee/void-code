import { existsSync, mkdirSync, mkdtempSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { SessionManager } from '../src/main/session-manager';
import type { TerminalProcess } from '../src/main/session-manager';
import { StatusChannelStore } from '../src/main/status-channel';
import { closeWorkspaceChat } from '../src/main/workspace-ipc';
import { WorkspaceStore } from '../src/main/workspace-store';

const ONE = '11111111-1111-4111-8111-111111111111';
const TWO = '22222222-2222-4222-8222-222222222222';
const THREE = '33333333-3333-4333-8333-333333333333';
const UNKNOWN = '44444444-4444-4444-8444-444444444444';
const roots: string[] = [];

class FakeProcess implements TerminalProcess {
  killed = false;
  write(): void { /* fixture */ }
  resize(): void { /* fixture */ }
  kill(): void { this.killed = true; }
  onData(): { dispose(): void } { return { dispose: () => undefined }; }
  onExit(): { dispose(): void } { return { dispose: () => undefined }; }
}

function fixture() {
  const root = mkdtempSync(path.join(os.tmpdir(), 'void-close-ipc-')); roots.push(root);
  const folder = path.join(root, 'workspace'); mkdirSync(folder);
  const workspace = new WorkspaceStore(path.join(root, 'workspace.json'));
  workspace.setFolder(folder); workspace.newChat(ONE); workspace.newChat(TWO); workspace.newChat(THREE);
  const channels = new StatusChannelStore(path.join(root, 'status'), () => undefined);
  const processes = new Map<string, FakeProcess>(); const directories = new Map<string, string>();
  const manager = new SessionManager((request, authority) => {
    const process = new FakeProcess(); processes.set(request.sessionId, process);
    directories.set(request.sessionId, path.dirname(authority!.path)); return process;
  }, () => undefined, channels);
  manager.start(7, { sessionId: ONE, cwd: folder, mode: 'create' });
  manager.start(7, { sessionId: TWO, cwd: folder, mode: 'create' });
  manager.start(9, { sessionId: THREE, cwd: folder, mode: 'create' });
  return { workspace, channels, manager, processes, directories };
}

afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

describe('workspace Close IPC authority', () => {
  it('destroys the owned runtime and channel before moving metadata to Recent', () => {
    const { workspace, channels, manager, processes, directories } = fixture();
    const closedDirectory = directories.get(ONE)!; const unrelatedDirectory = directories.get(TWO)!;

    const view = closeWorkspaceChat(manager, workspace, 7, { sessionId: ONE });

    expect(processes.get(ONE)?.killed).toBe(true);
    expect(() => manager.status(7, ONE)).toThrow('unknown session');
    expect(existsSync(closedDirectory)).toBe(false);
    expect(view.workspace?.tabs.find((tab) => tab.id === ONE)?.location).toBe('recent');
    expect(processes.get(TWO)?.killed).toBe(false);
    expect(manager.status(7, TWO)).toBe('running');
    expect(existsSync(unrelatedDirectory)).toBe(true);
    expect(channels.ingest(ONE, { version: 1, chatId: ONE, generation: 1, sequence: 1, state: 'Ready', timestamp: '2026-07-25T00:00:00Z' })).toBe(false);
    expect(() => manager.input(7, ONE, 'post-close')).toThrow('unknown session');

    expect(() => closeWorkspaceChat(manager, workspace, 7, { sessionId: ONE })).toThrow('unknown active chat');
    expect(() => closeWorkspaceChat(manager, workspace, 7, { sessionId: UNKNOWN })).toThrow('unknown active chat');
    expect(manager.status(7, TWO)).toBe('running');
  });

  it('rejects an owner mismatch without changing runtime or metadata', () => {
    const { workspace, manager, processes, directories } = fixture();
    expect(() => closeWorkspaceChat(manager, workspace, 7, { sessionId: THREE })).toThrow('unknown session');
    expect(processes.get(THREE)?.killed).toBe(false);
    expect(existsSync(directories.get(THREE)!)).toBe(true);
    expect(workspace.view().workspace?.tabs.find((tab) => tab.id === THREE)?.location).toBe('active');
  });
});

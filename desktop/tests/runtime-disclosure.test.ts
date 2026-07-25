import { mkdtempSync, mkdirSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { SessionManager } from '../src/main/session-manager';
import type { TerminalProcess } from '../src/main/session-manager';
import { WorkspaceStore } from '../src/main/workspace-store';

const ONE = '11111111-1111-4111-8111-111111111111';
const TWO = '22222222-2222-4222-8222-222222222222';
const THREE = '33333333-3333-4333-8333-333333333333';
const FOUR = '44444444-4444-4444-8444-444444444444';
const roots: string[] = [];

class FakeProcess implements TerminalProcess {
  write(): void { /* fixture */ }
  resize(): void { /* fixture */ }
  kill(): void { /* fixture */ }
  onData(): { dispose(): void } { return { dispose: () => undefined }; }
  onExit(): { dispose(): void } { return { dispose: () => undefined }; }
}

function fixture() {
  const root = mkdtempSync(path.join(os.tmpdir(), 'void-disclosure-')); roots.push(root);
  const folder = path.join(root, 'workspace'); mkdirSync(folder);
  return { folder, file: path.join(root, 'metadata', 'workspace.json') };
}
function request(sessionId: string, cwd: string, mode: 'create' | 'resume') { return { sessionId, cwd, mode } as const; }
function manager() { return new SessionManager(() => new FakeProcess(), () => undefined); }
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

describe('shared-files disclosure follows window-owned live runtimes', () => {
  it('warns when New Chat starts the second live runtime after relaunch with two active metadata records', () => {
    const { folder, file } = fixture(); const original = new WorkspaceStore(file);
    original.setFolder(folder); original.newChat(ONE); original.newChat(TWO); original.select(ONE);

    const relaunched = new WorkspaceStore(file); const sessions = manager();
    expect(relaunched.view().workspace?.tabs.filter((tab) => tab.location === 'active')).toHaveLength(2);
    expect(sessions.start(7, request(ONE, folder, 'resume')).showSharedFilesWarning).toBe(false);
    relaunched.newChat(THREE);
    expect(sessions.start(7, request(THREE, folder, 'create')).showSharedFilesWarning).toBe(true);
  });

  it('ignores Recent and sleeping records and does not warn for a third live runtime', () => {
    const { folder, file } = fixture(); const store = new WorkspaceStore(file);
    store.setFolder(folder); store.newChat(ONE); store.newChat(TWO); store.close(TWO); store.newChat(THREE);
    const sessions = manager();
    expect(sessions.start(8, request(ONE, folder, 'resume')).showSharedFilesWarning).toBe(false);
    store.newChat(FOUR);
    expect(sessions.start(8, request(FOUR, folder, 'create')).showSharedFilesWarning).toBe(true);
    expect(sessions.start(8, request(THREE, folder, 'resume')).showSharedFilesWarning).toBe(false);
  });

  it('acknowledges once across cleanup and restart transitions, then resets with the window owner', () => {
    const { folder } = fixture(); const sessions = manager();
    expect(sessions.start(9, request(ONE, folder, 'resume')).showSharedFilesWarning).toBe(false);
    expect(sessions.start(9, request(TWO, folder, 'create')).showSharedFilesWarning).toBe(true);
    sessions.stop(9, TWO);
    expect(sessions.start(9, request(TWO, folder, 'resume')).showSharedFilesWarning).toBe(false);
    sessions.stop(9, TWO);
    expect(sessions.start(9, request(THREE, folder, 'create')).showSharedFilesWarning).toBe(false);
    sessions.teardownOwner(9);
    expect(sessions.start(10, request(ONE, folder, 'resume')).showSharedFilesWarning).toBe(false);
    expect(sessions.start(10, request(FOUR, folder, 'create')).showSharedFilesWarning).toBe(true);
  });
});

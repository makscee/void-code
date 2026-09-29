import { existsSync, mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { WorkspaceStore } from '../src/main/workspace-store';

// Spec 2026-09-29-desktop-codex-chats-design, items 5, 6 and 9: a chat is Pi or
// Codex, chosen when it is created and never after. The choice lives on the tab
// (`runtime`, absent = pi), the Codex conversation it continues lives beside it
// (`codexSessionId`), and the last choice (`lastRuntime`, on the workspace
// record) is what New Chat offers next time. Every chat that exists today is
// Pi and has none of these fields; its file must keep reading exactly as before.
//
// Contract pinned here (WorkspaceStore, src/main/workspace-store.ts):
//   newChat(id, runtime?: 'pi' | 'codex')   runtime omitted = lastRuntime ?? 'pi'
//   bindCodexSession(chatId, codexSessionId) persists the Codex session of a Codex chat
//   assertLaunch(id, cwd)                    returns the tab (with runtime and codexSessionId)
//   view().workspace.lastRuntime            the last explicit choice

const ONE = '11111111-1111-4111-8111-111111111111';
const TWO = '22222222-2222-4222-8222-222222222222';
const THREE = '33333333-3333-4333-8333-333333333333';
// Codex issues UUIDv7 session ids.
const CODEX_SESSION = '01a0ec63-35c1-7d02-bc6a-c06464a61565';
const LATER_CODEX_SESSION = '01a0ec70-0000-7d02-bc6a-c06464a61565';

const roots: string[] = [];
function fixture() {
  const root = mkdtempSync(path.join(os.tmpdir(), 'void-codex-tabs-')); roots.push(root);
  const folder = path.join(root, 'workspace'); mkdirSync(folder);
  const file = path.join(root, 'metadata', 'workspace.json');
  mkdirSync(path.dirname(file), { recursive: true });
  return { folder, file };
}
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

function persisted(file: string): Record<string, unknown> { return JSON.parse(readFileSync(file, 'utf8')) as Record<string, unknown>; }
function write(file: string, value: unknown): void { writeFileSync(file, `${JSON.stringify(value, null, 2)}\n`); }
const runtimeOf = (tab: { runtime?: string } | undefined) => tab?.runtime ?? 'pi';

describe('workspace.json before and after Codex chats', () => {
  it('reads a file written before runtimes existed exactly as before: every chat is Pi', () => {
    const { folder, file } = fixture();
    const old = { version: 1, workspace: { path: folder, selectedId: ONE, tabs: [
      { id: ONE, title: 'Chat 1', location: 'active' }, { id: TWO, title: 'Old one', location: 'recent' },
    ] } };
    write(file, old);
    const store = new WorkspaceStore(file);
    const view = store.view();
    expect(existsSync(`${file}.invalid`), 'an old workspace.json was quarantined as invalid').toBe(false);
    expect(view.workspace?.tabs.map((tab) => [tab.id, tab.title, tab.location])).toEqual([[ONE, 'Chat 1', 'active'], [TWO, 'Old one', 'recent']]);
    for (const tab of view.workspace!.tabs) expect(runtimeOf(tab)).toBe('pi');
    expect(view.workspace?.tabs.some((tab) => 'codexSessionId' in tab && tab.codexSessionId !== undefined)).toBe(false);
  });

  it('reads Codex chats, their sessions and the last choice back from disk', () => {
    const { folder, file } = fixture();
    write(file, { version: 1, workspace: { path: folder, selectedId: TWO, lastRuntime: 'codex', tabs: [
      { id: ONE, title: 'Chat 1', location: 'recent' },
      { id: TWO, title: 'Chat 2', location: 'active', runtime: 'codex', codexSessionId: CODEX_SESSION },
      { id: THREE, title: 'Chat 3', location: 'active', runtime: 'codex' },
    ] } });
    const view = new WorkspaceStore(file).view();
    expect(existsSync(`${file}.invalid`)).toBe(false);
    expect(view.workspace?.lastRuntime).toBe('codex');
    const [one, two, three] = view.workspace!.tabs;
    expect(runtimeOf(one)).toBe('pi');
    expect(two).toMatchObject({ id: TWO, runtime: 'codex', codexSessionId: CODEX_SESSION });
    expect(three).toMatchObject({ id: THREE, runtime: 'codex' });
    expect(three.codexSessionId).toBeUndefined();
  });

  it.each([
    ['an unknown runtime', { runtime: 'claude' }],
    ['a runtime that is not a string', { runtime: 1 }],
    ['a Codex session that is not a UUID', { runtime: 'codex', codexSessionId: '../../etc/passwd' }],
    ['an empty Codex session', { runtime: 'codex', codexSessionId: '' }],
  ])('quarantines a tab with %s like any other invalid metadata', (_name, extra) => {
    const { folder, file } = fixture();
    write(file, { version: 1, workspace: { path: folder, selectedId: ONE, tabs: [{ id: ONE, title: 'Chat 1', location: 'active', ...extra }] } });
    const store = new WorkspaceStore(file);
    expect(store.view()).toEqual({ workspace: null, recoveryPath: null });
    expect(existsSync(`${file}.invalid`)).toBe(true);
  });

  it('quarantines an unknown lastRuntime', () => {
    const { folder, file } = fixture();
    write(file, { version: 1, workspace: { path: folder, selectedId: null, lastRuntime: 'claude', tabs: [] } });
    const store = new WorkspaceStore(file);
    expect(store.view()).toEqual({ workspace: null, recoveryPath: null });
    expect(existsSync(`${file}.invalid`)).toBe(true);
  });
});

describe('New Chat on Pi or on Codex', () => {
  it('stores a Codex chat as Codex and remembers the choice', () => {
    const { folder, file } = fixture(); const store = new WorkspaceStore(file); store.setFolder(folder);
    const view = store.newChat(ONE, 'codex');
    expect(view.workspace?.selectedId).toBe(ONE);
    expect(view.workspace?.tabs[0]).toMatchObject({ id: ONE, title: 'Chat 1', location: 'active', runtime: 'codex' });
    expect(view.workspace?.lastRuntime).toBe('codex');
    const onDisk = persisted(file) as { workspace: { lastRuntime?: string; tabs: Array<Record<string, unknown>> } };
    expect(onDisk.workspace.lastRuntime).toBe('codex');
    expect(onDisk.workspace.tabs[0]).toMatchObject({ id: ONE, runtime: 'codex' });
    expect(onDisk.workspace.tabs[0]).not.toHaveProperty('codexSessionId');
  });

  it('opens the next chat on the last runtime chosen', () => {
    const { folder, file } = fixture(); const store = new WorkspaceStore(file); store.setFolder(folder);
    store.newChat(ONE, 'codex');
    const view = store.newChat(TWO);
    expect(runtimeOf(view.workspace?.tabs[1])).toBe('codex');
    // And across a relaunch: the choice is on disk, not in memory.
    const relaunched = new WorkspaceStore(file);
    const again = relaunched.newChat(THREE);
    expect(runtimeOf(again.workspace?.tabs[2])).toBe('codex');
  });

  it('goes back to Pi when Pi is chosen, and remembers that', () => {
    const { folder, file } = fixture(); const store = new WorkspaceStore(file); store.setFolder(folder);
    store.newChat(ONE, 'codex');
    const pi = store.newChat(TWO, 'pi');
    expect(runtimeOf(pi.workspace?.tabs[1])).toBe('pi');
    expect(pi.workspace?.lastRuntime).toBe('pi');
    expect(runtimeOf(store.newChat(THREE).workspace?.tabs[2])).toBe('pi');
    // The Codex chat stays Codex: there is no switching inside a chat.
    expect(runtimeOf(store.view().workspace?.tabs[0])).toBe('codex');
  });

  it('defaults to Pi when nothing was ever chosen, without writing a runtime', () => {
    const { folder, file } = fixture(); const store = new WorkspaceStore(file); store.setFolder(folder);
    const view = store.newChat(ONE);
    expect(runtimeOf(view.workspace?.tabs[0])).toBe('pi');
    // The existing metadata tests pin that a default chat's file carries no
    // runtime word at all; absence is what "Pi" means.
    expect(JSON.stringify(persisted(file))).not.toMatch(/runtime/i);
  });

  it('refuses a runtime that does not exist', () => {
    const { folder, file } = fixture(); const store = new WorkspaceStore(file); store.setFolder(folder);
    expect(() => store.newChat(ONE, 'claude' as never)).toThrow();
    expect(store.view().workspace?.tabs).toEqual([]);
  });

  it('keeps the runtime through close, resume and rename', () => {
    const { folder, file } = fixture(); const store = new WorkspaceStore(file); store.setFolder(folder);
    store.newChat(ONE, 'codex'); store.newChat(TWO, 'pi');
    store.close(ONE); store.rename(ONE, 'Codex work'); store.resume(ONE);
    const tabs = new WorkspaceStore(file).view().workspace!.tabs;
    expect(tabs.find((tab) => tab.id === ONE)).toMatchObject({ runtime: 'codex', title: 'Codex work', location: 'active' });
    expect(runtimeOf(tabs.find((tab) => tab.id === TWO))).toBe('pi');
  });
});

describe('binding a Codex chat to its Codex session', () => {
  it('persists the session Codex reported, so the chat reopens the same conversation', () => {
    const { folder, file } = fixture(); const store = new WorkspaceStore(file); store.setFolder(folder);
    store.newChat(ONE, 'codex');
    store.bindCodexSession(ONE, CODEX_SESSION);
    expect(store.view().workspace?.tabs[0]).toMatchObject({ id: ONE, runtime: 'codex', codexSessionId: CODEX_SESSION });
    expect(new WorkspaceStore(file).view().workspace?.tabs[0]).toMatchObject({ codexSessionId: CODEX_SESSION });
  });

  it('follows the session Codex reports last', () => {
    const { folder, file } = fixture(); const store = new WorkspaceStore(file); store.setFolder(folder);
    store.newChat(ONE, 'codex');
    store.bindCodexSession(ONE, CODEX_SESSION);
    store.bindCodexSession(ONE, LATER_CODEX_SESSION);
    expect(new WorkspaceStore(file).view().workspace?.tabs[0].codexSessionId).toBe(LATER_CODEX_SESSION);
  });

  it('refuses to bind a Pi chat, an unknown chat or a session that is not a UUID', () => {
    const { folder, file } = fixture(); const store = new WorkspaceStore(file); store.setFolder(folder);
    store.newChat(ONE, 'pi'); store.newChat(TWO, 'codex');
    expect(() => store.bindCodexSession(ONE, CODEX_SESSION)).toThrow();
    expect(() => store.bindCodexSession(THREE, CODEX_SESSION)).toThrow();
    expect(() => store.bindCodexSession(TWO, 'not-a-session')).toThrow();
    const tabs = new WorkspaceStore(file).view().workspace!.tabs;
    expect(tabs.every((tab) => tab.codexSessionId === undefined)).toBe(true);
  });
});

describe('what a launch learns about the chat', () => {
  it('assertLaunch returns the tab, runtime and Codex session included', () => {
    const { folder, file } = fixture(); const store = new WorkspaceStore(file); store.setFolder(folder);
    store.newChat(ONE, 'codex'); store.bindCodexSession(ONE, CODEX_SESSION); store.newChat(TWO, 'pi');
    expect(store.assertLaunch(ONE, folder)).toMatchObject({ id: ONE, runtime: 'codex', codexSessionId: CODEX_SESSION });
    expect(runtimeOf(store.assertLaunch(TWO, folder))).toBe('pi');
  });
});

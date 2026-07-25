import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { WorkspaceStore } from '../src/main/workspace-store';

const ONE = '11111111-1111-4111-8111-111111111111';
const TWO = '22222222-2222-4222-8222-222222222222';
const roots: string[] = [];
function fixture() {
  const root = mkdtempSync(path.join(os.tmpdir(), 'void-tabs-')); roots.push(root);
  const folder = path.join(root, 'trusted workspace'); mkdirSync(folder);
  return { root, folder, file: path.join(root, 'metadata', 'workspace.json') };
}
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

describe('durable one-folder workspace metadata', () => {
  it('persists only folder, UUID, active/recent state, order, selection and title', () => {
    const { folder, file } = fixture(); const store = new WorkspaceStore(file);
    store.setFolder(folder); store.newChat(ONE); store.newChat(TWO); store.close(ONE);
    const persisted = JSON.parse(readFileSync(file, 'utf8')) as Record<string, unknown>;
    expect(persisted).toEqual({ version: 1, workspace: { path: folder, selectedId: TWO, tabs: [
      { id: ONE, title: 'Chat 1', location: 'recent' }, { id: TWO, title: 'Chat 2', location: 'active' },
    ] } });
    expect(JSON.stringify(persisted)).not.toMatch(/transcript|message|jsonl|cwd|process|auth/i);
  });

  it('moves Close to Recent, resumes the UUID in place, and never touches session files', () => {
    const { root, folder, file } = fixture(); const jsonl = path.join(root, `chat_${ONE}.jsonl`);
    mkdirSync(path.dirname(jsonl), { recursive: true }); writeFileSync(jsonl, 'conversation truth\n');
    const store = new WorkspaceStore(file); store.setFolder(folder); store.newChat(ONE); store.close(ONE);
    expect(store.view().workspace?.tabs[0].location).toBe('recent');
    store.resume(ONE);
    expect(store.view().workspace).toMatchObject({ selectedId: ONE, tabs: [{ id: ONE, location: 'active' }] });
    expect(readFileSync(jsonl, 'utf8')).toBe('conversation truth\n');
  });

  it('restores selected metadata while leaving process creation outside persistence', () => {
    const { folder, file } = fixture(); const first = new WorkspaceStore(file);
    first.setFolder(folder); first.newChat(ONE); first.newChat(TWO); first.select(ONE);
    const relaunched = new WorkspaceStore(file);
    expect(relaunched.view()).toEqual({ recoveryPath: null, workspace: { path: folder, selectedId: ONE, tabs: [
      { id: ONE, title: 'Chat 1', location: 'active' }, { id: TWO, title: 'Chat 2', location: 'active' },
    ] } });
    expect(readFileSync(file, 'utf8')).not.toContain('running');
  });

  it('blocks every operation and launch when a workspace moved, then supports relocate or remove without fallback cwd', () => {
    const { root, folder, file } = fixture(); const store = new WorkspaceStore(file);
    store.setFolder(folder); store.newChat(ONE); rmSync(folder, { recursive: true });
    expect(store.view().recoveryPath).toBe(folder);
    expect(() => store.newChat(TWO)).toThrow('recovery');
    expect(() => store.assertLaunch(ONE, os.homedir())).toThrow('recovery');
    const relocated = path.join(root, 'relocated'); mkdirSync(relocated); store.setFolder(relocated);
    expect(store.view().workspace).toMatchObject({ path: relocated, selectedId: ONE });
    expect(() => store.assertLaunch(ONE, folder)).toThrow('does not match');
    store.removeWorkspace(); expect(store.view()).toEqual({ workspace: null, recoveryPath: null });
  });

  it('keeps New Chat metadata free of runtime and disclosure truth', () => {
    const { folder, file } = fixture(); const store = new WorkspaceStore(file); store.setFolder(folder);
    const view = store.newChat(ONE);
    expect(view.workspace?.selectedId).toBe(ONE);
    expect(JSON.stringify(JSON.parse(readFileSync(file, 'utf8')))).not.toMatch(/running|runtime|warning|disclos/i);
  });
});

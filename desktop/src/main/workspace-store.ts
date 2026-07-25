import { existsSync, mkdirSync, readFileSync, renameSync, statSync, writeFileSync } from 'node:fs';
import path from 'node:path';

export type TabLocation = 'active' | 'recent';
export interface TabRecord { id: string; title: string; location: TabLocation }
export interface WorkspaceRecord { path: string; tabs: TabRecord[]; selectedId: string | null }
interface StoredState { version: 1; workspace: WorkspaceRecord | null }
export interface WorkspaceView { workspace: WorkspaceRecord | null; recoveryPath: string | null }

const emptyState = (): StoredState => ({ version: 1, workspace: null });
const UUID = /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

function parseState(raw: string): StoredState {
  const value = JSON.parse(raw) as unknown;
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error('invalid desktop metadata');
  const root = value as Record<string, unknown>;
  if (root.version !== 1 || !('workspace' in root)) throw new Error('invalid desktop metadata');
  if (root.workspace === null) return emptyState();
  if (typeof root.workspace !== 'object' || Array.isArray(root.workspace)) throw new Error('invalid workspace metadata');
  const workspace = root.workspace as Record<string, unknown>;
  if (typeof workspace.path !== 'string' || !path.isAbsolute(workspace.path) || !Array.isArray(workspace.tabs) || !(workspace.selectedId === null || typeof workspace.selectedId === 'string')) throw new Error('invalid workspace metadata');
  const ids = new Set<string>();
  const tabs = workspace.tabs.map((item): TabRecord => {
    if (typeof item !== 'object' || item === null || Array.isArray(item)) throw new Error('invalid tab metadata');
    const tab = item as Record<string, unknown>;
    if (typeof tab.id !== 'string' || !UUID.test(tab.id) || ids.has(tab.id) || typeof tab.title !== 'string' || tab.title.length < 1 || tab.title.length > 80 || (tab.location !== 'active' && tab.location !== 'recent')) throw new Error('invalid tab metadata');
    ids.add(tab.id); return { id: tab.id, title: tab.title, location: tab.location };
  });
  const selectedId = workspace.selectedId as string | null;
  if (selectedId !== null && !tabs.some((tab) => tab.id === selectedId && tab.location === 'active')) throw new Error('invalid selected tab');
  return { version: 1, workspace: { path: workspace.path, tabs, selectedId } };
}

export class WorkspaceStore {
  private state: StoredState;
  constructor(private readonly file: string, private readonly isDirectory = (candidate: string): boolean => {
    try { return existsSync(candidate) && statSync(candidate).isDirectory(); } catch { return false; }
  }) {
    try { this.state = parseState(readFileSync(file, 'utf8')); } catch (error) {
      if ((error as NodeJS.ErrnoException).code !== 'ENOENT') {
        try { renameSync(file, `${file}.invalid`); } catch { /* best effort quarantine */ }
      }
      this.state = emptyState();
    }
  }
  view(): WorkspaceView {
    const workspace = this.state.workspace;
    return { workspace: workspace ? structuredClone(workspace) : null, recoveryPath: workspace && !this.isDirectory(workspace.path) ? workspace.path : null };
  }
  setFolder(folder: string): WorkspaceView {
    if (!path.isAbsolute(folder) || !this.isDirectory(folder)) throw new Error('selected folder is unavailable');
    if (this.state.workspace) this.state.workspace.path = folder;
    else this.state.workspace = { path: folder, tabs: [], selectedId: null };
    this.save(); return this.view();
  }
  removeWorkspace(): WorkspaceView { this.state = emptyState(); this.save(); return this.view(); }
  newChat(id: string): WorkspaceView {
    const workspace = this.available();
    if (!UUID.test(id) || workspace.tabs.some((tab) => tab.id === id)) throw new Error('invalid chat UUID');
    const number = workspace.tabs.length + 1;
    workspace.tabs.push({ id, title: `Chat ${number}`, location: 'active' }); workspace.selectedId = id;
    this.save(); return this.view();
  }
  select(id: string): WorkspaceView { const workspace = this.available(); this.tab(workspace, id, 'active'); workspace.selectedId = id; this.save(); return this.view(); }
  close(id: string): WorkspaceView {
    const workspace = this.available(); const tab = this.tab(workspace, id, 'active'); tab.location = 'recent';
    if (workspace.selectedId === id) workspace.selectedId = workspace.tabs.find((candidate) => candidate.location === 'active')?.id ?? null;
    this.save(); return this.view();
  }
  resume(id: string): WorkspaceView { const workspace = this.available(); const tab = this.tab(workspace, id, 'recent'); tab.location = 'active'; workspace.selectedId = id; this.save(); return this.view(); }
  assertLaunch(id: string, cwd: string): void {
    const workspace = this.available();
    if (cwd !== workspace.path) throw new Error('chat cwd does not match its window workspace');
    this.tab(workspace, id, 'active');
  }
  private available(): WorkspaceRecord {
    const workspace = this.state.workspace;
    if (!workspace || !this.isDirectory(workspace.path)) throw new Error('workspace recovery is required');
    return workspace;
  }
  private tab(workspace: WorkspaceRecord, id: string, location: TabLocation): TabRecord {
    const tab = workspace.tabs.find((candidate) => candidate.id === id && candidate.location === location);
    if (!tab) throw new Error(`unknown ${location} chat`); return tab;
  }
  private save(): void {
    mkdirSync(path.dirname(this.file), { recursive: true });
    const temporary = `${this.file}.tmp`;
    writeFileSync(temporary, `${JSON.stringify(this.state, null, 2)}\n`, { mode: 0o600 }); renameSync(temporary, this.file);
  }
}

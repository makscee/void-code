import { existsSync, mkdirSync, readFileSync, renameSync, statSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { isChatRuntime, renameRequest, type ChatRuntime } from '../shared/contract';

export type TabLocation = 'active' | 'recent';
/** runtime absent = Pi; codexSessionId only on a Codex chat, once Codex has reported its session. */
export interface TabRecord { id: string; title: string; location: TabLocation; runtime?: ChatRuntime; codexSessionId?: string }
/** lastRuntime is the last explicit New Chat choice, the default for the next one. */
export interface WorkspaceRecord { path: string; tabs: TabRecord[]; selectedId: string | null; lastRuntime?: ChatRuntime }
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
    if ('runtime' in tab && !isChatRuntime(tab.runtime)) throw new Error('invalid tab metadata');
    if ('codexSessionId' in tab && (tab.runtime !== 'codex' || typeof tab.codexSessionId !== 'string' || !UUID.test(tab.codexSessionId))) throw new Error('invalid tab metadata');
    ids.add(tab.id);
    const record: TabRecord = { id: tab.id, title: tab.title, location: tab.location };
    if (tab.runtime !== undefined) record.runtime = tab.runtime as ChatRuntime;
    if (tab.codexSessionId !== undefined) record.codexSessionId = tab.codexSessionId as string;
    return record;
  });
  if ('lastRuntime' in workspace && !isChatRuntime(workspace.lastRuntime)) throw new Error('invalid workspace metadata');
  const selectedId = workspace.selectedId as string | null;
  if (selectedId !== null && !tabs.some((tab) => tab.id === selectedId && tab.location === 'active')) throw new Error('invalid selected tab');
  const record: WorkspaceRecord = { path: workspace.path, tabs, selectedId };
  if (workspace.lastRuntime !== undefined) record.lastRuntime = workspace.lastRuntime as ChatRuntime;
  return { version: 1, workspace: record };
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
  /** A chat on runtime, or on the last runtime chosen when none is given (Pi when none ever was). */
  newChat(id: string, runtime?: ChatRuntime): WorkspaceView {
    if (runtime !== undefined && !isChatRuntime(runtime)) throw new Error('invalid chat runtime');
    const workspace = this.available();
    if (!UUID.test(id) || workspace.tabs.some((tab) => tab.id === id)) throw new Error('invalid chat UUID');
    const number = workspace.tabs.length + 1;
    const tab: TabRecord = { id, title: `Chat ${number}`, location: 'active' };
    // Absence is what Pi means, so a Pi chat's record carries no runtime at all.
    if ((runtime ?? workspace.lastRuntime) === 'codex') tab.runtime = 'codex';
    if (runtime !== undefined) workspace.lastRuntime = runtime;
    workspace.tabs.push(tab); workspace.selectedId = id;
    this.save(); return this.view();
  }
  /** Records the Codex conversation a Codex chat continues; the latest report wins. */
  bindCodexSession(id: string, codexSessionId: string): WorkspaceView {
    const workspace = this.state.workspace;
    if (!workspace) throw new Error('no workspace');
    if (typeof codexSessionId !== 'string' || !UUID.test(codexSessionId)) throw new Error('invalid Codex session');
    const tab = workspace.tabs.find((candidate) => candidate.id === id);
    if (!tab || tab.runtime !== 'codex') throw new Error('unknown Codex chat');
    if (tab.codexSessionId === codexSessionId) return this.view();
    const previous = tab.codexSessionId;
    tab.codexSessionId = codexSessionId;
    try { this.save(); } catch (error) {
      if (previous === undefined) delete tab.codexSessionId; else tab.codexSessionId = previous;
      throw error;
    }
    return this.view();
  }
  select(id: string): WorkspaceView { const workspace = this.available(); this.tab(workspace, id, 'active'); workspace.selectedId = id; this.save(); return this.view(); }
  rename(id: string, title: string): WorkspaceView {
    const request = renameRequest({ sessionId: id, title });
    const workspace = this.available();
    const tab = workspace.tabs.find((candidate) => candidate.id === request.sessionId);
    if (!tab) throw new Error('unknown chat');
    const previousTitle = tab.title;
    tab.title = request.title;
    try { this.save(); } catch (error) {
      tab.title = previousTitle;
      throw error;
    }
    return this.view();
  }
  assertClose(id: string): void { this.tab(this.available(), id, 'active'); }
  close(id: string): WorkspaceView {
    const workspace = this.available(); const tab = this.tab(workspace, id, 'active'); tab.location = 'recent';
    if (workspace.selectedId === id) workspace.selectedId = workspace.tabs.find((candidate) => candidate.location === 'active')?.id ?? null;
    this.save(); return this.view();
  }
  resume(id: string): WorkspaceView { const workspace = this.available(); const tab = this.tab(workspace, id, 'recent'); tab.location = 'active'; workspace.selectedId = id; this.save(); return this.view(); }
  /** The chat a launch is for, runtime and Codex session included; throws unless it may launch. */
  assertLaunch(id: string, cwd: string): TabRecord {
    const workspace = this.available();
    if (cwd !== workspace.path) throw new Error('chat cwd does not match its window workspace');
    return structuredClone(this.tab(workspace, id, 'active'));
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

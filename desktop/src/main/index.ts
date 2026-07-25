import { app, BrowserWindow, dialog, ipcMain, shell, webContents } from 'electron';
import { statSync, writeFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import * as pty from 'node-pty';
import { IPC, chatRequest, inputRequest, linkRequest, resizeRequest, sessionRequest, startRequest, subscribeRequest } from '../shared/contract';
import type { RealStartRequest, StartRequest } from '../shared/contract';
import { resolvePrivateRuntime } from './resources';
import { sessionLifecycleArgs } from './session-files';
import type { PrivateRuntime } from './resources';
import { SessionManager, wrapPty } from './session-manager';
import { StatusChannelStore } from './status-channel';
import type { StatusWriteAuthority } from './status-channel';
import { closeWorkspaceChat } from './workspace-ipc';
import { WorkspaceStore } from './workspace-store';

const smokeArgument = process.argv.find((argument) => argument.startsWith('--void-smoke-output='));
const smokeOutput = smokeArgument?.slice('--void-smoke-output='.length);
const runtimeRoot = path.join(process.resourcesPath, 'private-runtime');
let manager: SessionManager;
let workspace: WorkspaceStore;

function spawnRequest(runtime: PrivateRuntime, request: StartRequest, authority?: StatusWriteAuthority) {
  if ('fixture' in request) return wrapPty(pty.spawn(runtime.node, [runtime.fixture], {
    name: 'xterm-256color', cols: 80, rows: 24, cwd: runtime.root, env: { PATH: '/usr/bin:/bin', VOID_FIXTURE: 'owned' },
  }));
  const real = request as RealStartRequest;
  if (!statSync(real.cwd).isDirectory()) throw new Error('selected folder is unavailable');
  const sessionsRoot = path.join(os.homedir(), '.pi/agent/sessions');
  const lifecycle = sessionLifecycleArgs(sessionsRoot, real.sessionId, real.mode);
  const args = ['desktop-session', '--node', runtime.node, '--pi-entry', runtime.piEntry, '--', ...lifecycle];
  return wrapPty(pty.spawn(runtime.vc, args, {
    name: 'xterm-256color', cols: 100, rows: 30, cwd: real.cwd,
    env: {
      ...process.env, PATH: `${path.dirname(runtime.node)}:/usr/bin:/bin`, TERM: 'xterm-256color', COLORTERM: 'truecolor',
      ...(authority ? { VC_DESKTOP_STATUS_PATH: authority.path, VC_DESKTOP_CHAT_ID: authority.chatId, VC_DESKTOP_STATUS_GENERATION: String(authority.generation) } : {}),
    },
  }));
}

function registerIpc(): void {
  ipcMain.handle(IPC.start, (event, raw: unknown) => {
    const request = startRequest(raw);
    if (!('fixture' in request)) workspace.assertLaunch(request.sessionId, request.cwd);
    return { sessionId: request.sessionId, ...manager.start(event.sender.id, request) };
  });
  ipcMain.handle(IPC.input, (event, raw: unknown) => { const request = inputRequest(raw); manager.input(event.sender.id, request.sessionId, request.data); });
  ipcMain.handle(IPC.resize, (event, raw: unknown) => { const request = resizeRequest(raw); manager.resize(event.sender.id, request.sessionId, request.cols, request.rows); });
  ipcMain.handle(IPC.stop, (event, raw: unknown) => { const request = sessionRequest(raw); manager.stop(event.sender.id, request.sessionId); });
  ipcMain.handle(IPC.status, (event, raw: unknown) => { const request = sessionRequest(raw); return { sessionId: request.sessionId, status: manager.status(event.sender.id, request.sessionId) }; });
  ipcMain.handle(IPC.lifecycleStatus, (event, raw: unknown) => { const request = sessionRequest(raw); return { sessionId: request.sessionId, status: manager.lifecycleStatus(event.sender.id, request.sessionId) }; });
  ipcMain.handle(IPC.chooseFolder, async () => { const result = await dialog.showOpenDialog({ properties: ['openDirectory', 'createDirectory'] }); return result.canceled ? null : result.filePaths[0]; });
  ipcMain.handle(IPC.workspaceLoad, () => workspace.view());
  ipcMain.handle(IPC.workspaceChoose, async () => {
    const current = workspace.view();
    if (current.workspace && !current.recoveryPath) throw new Error('this window already owns a folder');
    const result = await dialog.showOpenDialog({ properties: ['openDirectory', 'createDirectory'] });
    return result.canceled ? null : workspace.setFolder(result.filePaths[0]);
  });
  ipcMain.handle(IPC.workspaceRemove, () => workspace.removeWorkspace());
  ipcMain.handle(IPC.workspaceNewChat, () => ({ view: workspace.newChat(randomUUID()) }));
  ipcMain.handle(IPC.workspaceSelect, (event, raw: unknown) => {
    const selected = chatRequest(raw).sessionId;
    const view = workspace.select(selected);
    try { manager.clearUnread(event.sender.id, selected); } catch { /* sleeping chat has no live status channel */ }
    return view;
  });
  ipcMain.handle(IPC.workspaceClose, (event, raw: unknown) => closeWorkspaceChat(manager, workspace, event.sender.id, raw));
  ipcMain.handle(IPC.workspaceResume, (_event, raw: unknown) => workspace.resume(chatRequest(raw).sessionId));
  ipcMain.handle(IPC.openLink, async (_event, raw: unknown) => shell.openExternal(linkRequest(raw)));
  ipcMain.on(IPC.subscribe, (event, raw: unknown) => { try { manager.subscribe(event.sender.id, subscribeRequest(raw)); event.returnValue = { ok: true }; } catch (error) { event.returnValue = { ok: false, error: error instanceof Error ? error.message : 'subscription rejected' }; } });
  ipcMain.on(IPC.unsubscribe, (event, raw: unknown) => { try { manager.unsubscribe(event.sender.id, subscribeRequest(raw)); } catch { /* stale unsubscribe is inert */ } });
}

async function createWindow(): Promise<void> {
  const window = new BrowserWindow({
    show: !smokeOutput, width: 1100, height: 760, backgroundColor: '#101216',
    webPreferences: { preload: path.join(__dirname, '../preload/index.js'), contextIsolation: true, nodeIntegration: false, sandbox: false },
  });
  const ownerId = window.webContents.id;
  window.webContents.on('did-start-navigation', () => manager.teardownOwner(ownerId));
  window.webContents.on('render-process-gone', () => manager.teardownOwner(ownerId));
  window.webContents.on('destroyed', () => manager.teardownOwner(ownerId));
  if (smokeOutput) {
    window.webContents.on('page-title-updated', (event, title) => {
      if (!title.startsWith('VOID_SMOKE:')) return;
      event.preventDefault(); const result = JSON.parse(title.slice('VOID_SMOKE:'.length)) as { ok: boolean };
      writeFileSync(smokeOutput, `${JSON.stringify(result, null, 2)}\n`, { mode: 0o600 }); manager.teardownOwner(ownerId); app.exit(result.ok ? 0 : 1);
    });
    await window.loadFile(path.join(__dirname, '../renderer/smoke.html'));
  } else await window.loadFile(path.join(__dirname, '../renderer/index.html'));
}

void app.whenReady().then(async () => {
  const runtime = resolvePrivateRuntime(runtimeRoot);
  workspace = new WorkspaceStore(path.join(app.getPath('userData'), 'workspace.json'));
  const statusChannels = new StatusChannelStore(
    path.join(app.getPath('userData'), 'status-channels'),
    (ownerId, event) => manager?.lifecycleChanged(ownerId, event),
    (chatId) => workspace.view().workspace?.selectedId === chatId,
  );
  manager = new SessionManager((request, authority) => spawnRequest(runtime, request, authority), (ownerId, channel, payload) => webContents.fromId(ownerId)?.send(channel, payload), statusChannels);
  registerIpc(); await createWindow();
});
app.on('before-quit', () => manager?.teardownAll());
app.on('window-all-closed', () => app.quit());

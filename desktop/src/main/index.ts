import { app, BrowserWindow, dialog, ipcMain, shell, webContents } from 'electron';
import { statSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import * as pty from 'node-pty';
import { IPC, inputRequest, linkRequest, resizeRequest, sessionRequest, startRequest, subscribeRequest } from '../shared/contract';
import type { RealStartRequest, StartRequest } from '../shared/contract';
import { resolvePrivateRuntime } from './resources';
import { sessionLifecycleArgs } from './session-files';
import type { PrivateRuntime } from './resources';
import { SessionManager, wrapPty } from './session-manager';

const smokeArgument = process.argv.find((argument) => argument.startsWith('--void-smoke-output='));
const smokeOutput = smokeArgument?.slice('--void-smoke-output='.length);
const runtimeRoot = path.join(process.resourcesPath, 'private-runtime');
let manager: SessionManager;

function spawnRequest(runtime: PrivateRuntime, request: StartRequest) {
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
    env: { ...process.env, PATH: `${path.dirname(runtime.node)}:/usr/bin:/bin`, TERM: 'xterm-256color', COLORTERM: 'truecolor' },
  }));
}

function registerIpc(): void {
  ipcMain.handle(IPC.start, (event, raw: unknown) => { const request = startRequest(raw); return { sessionId: request.sessionId, status: manager.start(event.sender.id, request) }; });
  ipcMain.handle(IPC.input, (event, raw: unknown) => { const request = inputRequest(raw); manager.input(event.sender.id, request.sessionId, request.data); });
  ipcMain.handle(IPC.resize, (event, raw: unknown) => { const request = resizeRequest(raw); manager.resize(event.sender.id, request.sessionId, request.cols, request.rows); });
  ipcMain.handle(IPC.stop, (event, raw: unknown) => { const request = sessionRequest(raw); manager.stop(event.sender.id, request.sessionId); });
  ipcMain.handle(IPC.status, (event, raw: unknown) => { const request = sessionRequest(raw); return { sessionId: request.sessionId, status: manager.status(event.sender.id, request.sessionId) }; });
  ipcMain.handle(IPC.chooseFolder, async () => { const result = await dialog.showOpenDialog({ properties: ['openDirectory', 'createDirectory'] }); return result.canceled ? null : result.filePaths[0]; });
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
  manager = new SessionManager((request) => spawnRequest(runtime, request), (ownerId, channel, payload) => webContents.fromId(ownerId)?.send(channel, payload));
  registerIpc(); await createWindow();
});
app.on('before-quit', () => manager?.teardownAll());
app.on('window-all-closed', () => app.quit());

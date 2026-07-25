import { app, BrowserWindow, ipcMain, webContents } from 'electron';
import { writeFileSync } from 'node:fs';
import path from 'node:path';
import * as pty from 'node-pty';
import { IPC, inputRequest, resizeRequest, sessionRequest, startRequest, subscribeRequest } from '../shared/contract';
import { resolvePrivateRuntime } from './resources';
import { SessionManager, wrapPty } from './session-manager';

const smokeArgument = process.argv.find((argument) => argument.startsWith('--void-smoke-output='));
const smokeOutput = smokeArgument?.slice('--void-smoke-output='.length);
const runtimeRoot = path.join(process.resourcesPath, 'private-runtime');
let manager: SessionManager;

function registerIpc(): void {
  ipcMain.handle(IPC.start, (event, raw: unknown) => {
    const request = startRequest(raw);
    return { sessionId: request.sessionId, status: manager.start(event.sender.id, request.sessionId) };
  });
  ipcMain.handle(IPC.input, (event, raw: unknown) => { const request = inputRequest(raw); manager.input(event.sender.id, request.sessionId, request.data); });
  ipcMain.handle(IPC.resize, (event, raw: unknown) => { const request = resizeRequest(raw); manager.resize(event.sender.id, request.sessionId, request.cols, request.rows); });
  ipcMain.handle(IPC.stop, (event, raw: unknown) => { const request = sessionRequest(raw); manager.stop(event.sender.id, request.sessionId); });
  ipcMain.handle(IPC.status, (event, raw: unknown) => { const request = sessionRequest(raw); return { sessionId: request.sessionId, status: manager.status(event.sender.id, request.sessionId) }; });
  ipcMain.on(IPC.subscribe, (event, raw: unknown) => {
    try { manager.subscribe(event.sender.id, subscribeRequest(raw)); event.returnValue = { ok: true }; }
    catch (error) { event.returnValue = { ok: false, error: error instanceof Error ? error.message : 'subscription rejected' }; }
  });
  ipcMain.on(IPC.unsubscribe, (event, raw: unknown) => {
    try { manager.unsubscribe(event.sender.id, subscribeRequest(raw)); } catch { /* stale unsubscribe is intentionally inert */ }
  });
}

async function createWindow(): Promise<void> {
  const window = new BrowserWindow({
    show: !smokeOutput,
    webPreferences: {
      preload: path.join(__dirname, '../preload/index.js'),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: false,
    },
  });
  const ownerId = window.webContents.id;
  window.webContents.on('destroyed', () => manager.teardownOwner(ownerId));
  if (smokeOutput) {
    window.webContents.on('page-title-updated', (event, title) => {
      if (!title.startsWith('VOID_SMOKE:')) return;
      event.preventDefault();
      const result = JSON.parse(title.slice('VOID_SMOKE:'.length)) as { ok: boolean };
      writeFileSync(smokeOutput, `${JSON.stringify(result, null, 2)}\n`, { mode: 0o600 });
      manager.teardownOwner(ownerId);
      app.exit(result.ok ? 0 : 1);
    });
    await window.loadFile(path.join(__dirname, '../renderer/smoke.html'));
  } else await window.loadFile(path.join(__dirname, '../renderer/index.html'));
}

void app.whenReady().then(async () => {
  const runtime = resolvePrivateRuntime(runtimeRoot);
  manager = new SessionManager(
    () => wrapPty(pty.spawn(runtime.node, [runtime.fixture], {
      name: 'xterm-256color', cols: 80, rows: 24, cwd: runtime.root,
      env: { PATH: '/usr/bin:/bin', VOID_FIXTURE: 'owned' },
    })),
    (ownerId, channel, payload) => webContents.fromId(ownerId)?.send(channel, payload),
  );
  registerIpc();
  await createWindow();
});
app.on('window-all-closed', () => app.quit());

import { app, BrowserWindow, clipboard, dialog, ipcMain, nativeImage, shell, webContents } from 'electron';
import type { IpcMainEvent, IpcMainInvokeEvent } from 'electron';
import { existsSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { randomUUID } from 'node:crypto';
import os from 'node:os';
import path from 'node:path';
import * as pty from 'node-pty';
import { IPC, chatRequest, inputRequest, linkRequest, resizeRequest, sessionRequest, startRequest, subscribeRequest, supportRequest } from '../shared/contract';
import type { StartRequest } from '../shared/contract';
import { resolvePrivateRuntime } from './resources';
import { spawnDesktopRequest } from './spawn-request';
import type { PrivateRuntime } from './resources';
import { readAuthStatus } from './auth-session';
import { startAuthLogin } from './auth-ipc';
import { spawnAuthProcess } from './auth-spawn';
import { SessionManager, wrapPty } from './session-manager';
import { StatusChannelStore } from './status-channel';
import { buildSupportReport, copySupportReport, saveSupportReport } from './support-report';
import type { StatusWriteAuthority } from './status-channel';
import { closeWorkspaceChat } from './workspace-ipc';
import { WorkspaceStore } from './workspace-store';
import { installNavigationPolicy, rendererAuthority, rendererUrl } from './renderer-authority';
import { startupDiagnostic, startupDialogMessage, writeStartupDiagnostic } from './startup-diagnostic';
import { focusExistingWindow, loadAndPresentWindow, loadRenderer, missingRendererRequested, rendererFilename, runBootstrap, startupStage } from './startup-lifecycle';
import type { StartupStageError } from './startup-lifecycle';

const smokeArgument = process.argv.find((argument) => argument.startsWith('--void-smoke-output='));
const smokeOutput = smokeArgument?.slice('--void-smoke-output='.length);
const productionProbeArgument = process.argv.find((argument) => argument.startsWith('--void-production-terminal-output='));
const productionProbeOutput = productionProbeArgument?.slice('--void-production-terminal-output='.length);
const productionProbePerturb = productionProbeOutput ? process.argv.find((argument) => argument.startsWith('--void-production-terminal-perturb='))?.slice('--void-production-terminal-perturb='.length) : undefined;
if (productionProbePerturb && !['missing-font', 'palette-collapse'].includes(productionProbePerturb)) throw new Error('unknown production terminal perturbation');
const productionProbeRoot = productionProbeOutput ? mkdtempSync(path.join(os.tmpdir(), 'void-code-production-terminal-')) : undefined;
if (productionProbeRoot) app.setPath('userData', path.join(productionProbeRoot, 'user-data'));
const runtimeRoot = path.join(process.resourcesPath, 'private-runtime');
const missingRendererTest = missingRendererRequested(process.argv, process.env.VOID_STARTUP_TEST_MISSING_RENDERER, existsSync(path.join(app.getPath('userData'), '.void-startup-test-missing-renderer')));
let manager: SessionManager;
let workspace: WorkspaceStore;
let mainWindow: BrowserWindow | undefined;
let runtime: PrivateRuntime;

function spawnRequest(runtime: PrivateRuntime, request: StartRequest, authority?: StatusWriteAuthority) {
  return wrapPty(spawnDesktopRequest(runtime, request, pty.spawn, authority));
}

function supportReport(raw: unknown) {
  const context = supportRequest(raw);
  const current = workspace.view();
  const workspaceState = !current.workspace ? 'none' : current.recoveryPath ? 'missing' : 'ready';
  return buildSupportReport({
    appVersion: app.getVersion(), platform: process.platform, architecture: process.arch, generatedAt: new Date().toISOString(),
    workspace: workspaceState, runtime: context.runtime, recoveryCode: context.recoveryCode,
  });
}

function assertRenderer(event: IpcMainInvokeEvent | IpcMainEvent): void {
  if (!mainWindow) throw new Error('renderer authority rejected');
  const page = smokeOutput ? 'smoke.html' : 'index.html';
  const query = productionProbeOutput ? { productionTerminalProbe: '1', ...(productionProbePerturb ? { productionTerminalPerturb: productionProbePerturb } : {}) } : undefined;
  rendererAuthority(mainWindow.webContents, rendererUrl(path.join(__dirname, `../renderer/${page}`), query))(event);
}

function registerIpc(): void {
  ipcMain.handle(IPC.start, (event, raw: unknown) => { assertRenderer(event);
    const request = startRequest(raw);
    if (!('fixture' in request)) workspace.assertLaunch(request.sessionId, request.cwd);
    return { sessionId: request.sessionId, ...manager.start(event.sender.id, request) };
  });
  ipcMain.handle(IPC.input, (event, raw: unknown) => { assertRenderer(event); const request = inputRequest(raw); manager.input(event.sender.id, request.sessionId, request.data); });
  ipcMain.handle(IPC.resize, (event, raw: unknown) => { assertRenderer(event); const request = resizeRequest(raw); manager.resize(event.sender.id, request.sessionId, request.cols, request.rows); });
  ipcMain.handle(IPC.stop, (event, raw: unknown) => { assertRenderer(event); const request = sessionRequest(raw); manager.stop(event.sender.id, request.sessionId); });
  ipcMain.handle(IPC.status, (event, raw: unknown) => { assertRenderer(event); const request = sessionRequest(raw); return { sessionId: request.sessionId, status: manager.status(event.sender.id, request.sessionId) }; });
  ipcMain.handle(IPC.lifecycleStatus, (event, raw: unknown) => { assertRenderer(event); const request = sessionRequest(raw); return { sessionId: request.sessionId, status: manager.lifecycleStatus(event.sender.id, request.sessionId) }; });
  ipcMain.handle(IPC.chooseFolder, async (event) => { assertRenderer(event); const result = await dialog.showOpenDialog({ properties: ['openDirectory', 'createDirectory'] }); return result.canceled ? null : result.filePaths[0]; });
  ipcMain.handle(IPC.workspaceLoad, (event) => { assertRenderer(event); return workspace.view(); });
  ipcMain.handle(IPC.workspaceChoose, async (event) => { assertRenderer(event);
    const current = workspace.view();
    if (current.workspace && !current.recoveryPath) throw new Error('this window already owns a folder');
    const result = await dialog.showOpenDialog({ properties: ['openDirectory', 'createDirectory'] });
    return result.canceled ? null : workspace.setFolder(result.filePaths[0]);
  });
  ipcMain.handle(IPC.workspaceRemove, (event) => { assertRenderer(event); return workspace.removeWorkspace(); });
  ipcMain.handle(IPC.workspaceNewChat, (event) => { assertRenderer(event); return { view: workspace.newChat(randomUUID()) }; });
  ipcMain.handle(IPC.workspaceSelect, (event, raw: unknown) => { assertRenderer(event);
    const selected = chatRequest(raw).sessionId;
    const view = workspace.select(selected);
    try { manager.clearUnread(event.sender.id, selected); } catch { /* sleeping chat has no live status channel */ }
    return view;
  });
  ipcMain.handle(IPC.workspaceClose, (event, raw: unknown) => { assertRenderer(event); return closeWorkspaceChat(manager, workspace, event.sender.id, raw); });
  ipcMain.handle(IPC.workspaceResume, (event, raw: unknown) => { assertRenderer(event); return workspace.resume(chatRequest(raw).sessionId); });
  ipcMain.handle(IPC.openLink, async (event, raw: unknown) => { assertRenderer(event); return shell.openExternal(linkRequest(raw)); });
  ipcMain.handle(IPC.supportCopy, (event, raw: unknown) => { assertRenderer(event); return copySupportReport(supportReport(raw), (text) => clipboard.writeText(text)); });
  ipcMain.handle(IPC.supportSave, async (event, raw: unknown) => { assertRenderer(event);
    const report = supportReport(raw);
    const stamp = report.generatedAt.slice(0, 19).replaceAll(':', '-');
    return saveSupportReport(
      report,
      async () => { const result = await dialog.showSaveDialog({ defaultPath: `Void-Code-Support-${stamp}.json`, filters: [{ name: 'JSON', extensions: ['json'] }] }); return result.canceled ? null : result.filePath ?? null; },
      (file, text) => writeFileSync(file, text, { encoding: 'utf8', mode: 0o600 }),
    );
  });
  ipcMain.handle(IPC.authStatus, (event) => { assertRenderer(event); return readAuthStatus(runtime.vc, spawnAuthProcess); });
  ipcMain.handle(IPC.authLoginStart, (event) => { assertRenderer(event);
    const ownerId = event.sender.id;
    const loginId = randomUUID();
    startAuthLogin(loginId, runtime.vc, spawnAuthProcess, (push) => { webContents.fromId(ownerId)?.send(IPC.authLoginEvent, push); }, (url) => { void shell.openExternal(url); });
    return { loginId };
  });
  ipcMain.on(IPC.subscribe, (event, raw: unknown) => { try { assertRenderer(event); manager.subscribe(event.sender.id, subscribeRequest(raw)); event.returnValue = { ok: true }; } catch (error) { event.returnValue = { ok: false, error: error instanceof Error ? error.message : 'subscription rejected' }; } });
  ipcMain.on(IPC.unsubscribe, (event, raw: unknown) => { assertRenderer(event); try { manager.unsubscribe(event.sender.id, subscribeRequest(raw)); } catch { /* stale unsubscribe is inert */ } });
}

function pixelContrast(red: number, green: number, blue: number, background: number[]): number {
  const luminance = (channels: number[]) => channels.map((value) => { const normalized = value / 255; return normalized <= 0.04045 ? normalized / 12.92 : ((normalized + 0.055) / 1.055) ** 2.4; }).reduce((sum, value, index) => sum + value * [0.2126, 0.7152, 0.0722][index], 0);
  const foreground = luminance([red, green, blue]); const backdrop = luminance(background);
  return (Math.max(foreground, backdrop) + 0.05) / (Math.min(foreground, backdrop) + 0.05);
}
function hueBin(red: number, green: number, blue: number): number | undefined {
  const values = [red / 255, green / 255, blue / 255]; const maximum = Math.max(...values); const minimum = Math.min(...values); const delta = maximum - minimum;
  if (maximum === 0 || delta / maximum < 0.12) return undefined;
  const hue = maximum === values[0] ? ((values[1] - values[2]) / delta) % 6 : maximum === values[1] ? (values[2] - values[0]) / delta + 2 : (values[0] - values[1]) / delta + 4;
  return Math.floor((((hue * 60) + 360) % 360) / 30);
}
async function captureVisibleColors(window: BrowserWindow, raw: unknown) {
  const request = raw as { requestId?: unknown; clip?: { x?: unknown; y?: unknown; width?: unknown; height?: unknown }; background?: unknown };
  const coordinates = [request.clip?.x, request.clip?.y, request.clip?.width, request.clip?.height].map(Number);
  if (typeof request.requestId !== 'string' || coordinates.some((value) => !Number.isFinite(value)) || coordinates[2] <= 0 || coordinates[3] <= 0 || typeof request.background !== 'string' || !/^#[0-9a-f]{6}$/i.test(request.background)) throw new Error('invalid production pixel request');
  const backgroundHex = request.background as string;
  const background = [1, 3, 5].map((offset) => Number.parseInt(backgroundHex.slice(offset, offset + 2), 16));
  const debug = window.webContents.debugger; let attached = false;
  try {
    debug.attach('1.3'); attached = true;
    const response = await debug.sendCommand('Page.captureScreenshot', { format: 'png', fromSurface: true, captureBeyondViewport: false, clip: { x: coordinates[0], y: coordinates[1], width: coordinates[2], height: coordinates[3], scale: 1 } }) as { data: string };
    const image = nativeImage.createFromBuffer(Buffer.from(response.data, 'base64')); const bitmap = image.toBitmap(); const colors = new Map<string, number>(); let contrastingPixels = 0; let maxContrast = 0;
    for (let offset = 0; offset < bitmap.length; offset += 4) {
      const blue = bitmap[offset]; const green = bitmap[offset + 1]; const red = bitmap[offset + 2]; const ratio = pixelContrast(red, green, blue, background);
      if (ratio < 2) continue; contrastingPixels++; maxContrast = Math.max(maxContrast, ratio); const key = `${red},${green},${blue}`; colors.set(key, (colors.get(key) ?? 0) + 1);
    }
    const visible = [...colors.entries()].filter(([, count]) => count >= 2).sort((first, second) => second[1] - first[1]); const hueBins = new Set<number>();
    for (const [color] of visible) { const values = color.split(',').map(Number); const bin = hueBin(values[0], values[1], values[2]); if (bin !== undefined) hueBins.add(bin); }
    return { requestId: request.requestId, summary: { source: 'cdp-bitmap' as const, distinctVisibleRgb: visible.length, contrastingPixels, maxContrast, chromaticHueBins: hueBins.size, visibleRgb: visible.slice(0, 12).map(([color]) => color) } };
  } finally { if (attached) debug.detach(); }
}

async function createWindow(): Promise<BrowserWindow> {
  const window = await startupStage('window-creation', () => new BrowserWindow({
    title: 'Void Code', show: false, width: 1100, height: 760, backgroundColor: '#101216',
    webPreferences: { preload: path.join(__dirname, '../preload/index.js'), contextIsolation: true, nodeIntegration: false, sandbox: true },
  }));
  // Renderer IPC begins during load, so authority must name this exact window before loading it.
  mainWindow = window;
  const ownerId = window.webContents.id;
  installNavigationPolicy(window.webContents);
  window.webContents.on('did-start-navigation', () => manager.teardownOwner(ownerId));
  window.webContents.on('render-process-gone', () => manager.teardownOwner(ownerId));
  window.webContents.on('destroyed', () => manager.teardownOwner(ownerId));
  const headless = smokeOutput ? { output: smokeOutput, prefix: 'VOID_SMOKE:', page: 'smoke.html', query: undefined }
    : productionProbeOutput ? { output: productionProbeOutput, prefix: 'VOID_PRODUCTION_TERMINAL:', page: 'index.html', query: { productionTerminalProbe: '1' } } : undefined;
  if (headless) {
    window.webContents.on('page-title-updated', (event, title) => {
      const pixelPrefix = 'VOID_PRODUCTION_PIXEL_REQUEST:';
      if (productionProbeOutput && title.startsWith(pixelPrefix)) {
        event.preventDefault();
        void captureVisibleColors(window, JSON.parse(title.slice(pixelPrefix.length))).then((reply) => window.webContents.executeJavaScript(`window.__resolveProductionPixelProbe?.(${JSON.stringify(reply)})`));
        return;
      }
      const viewportPrefix = 'VOID_PRODUCTION_VIEWPORT_REQUEST:';
      if (productionProbeOutput && title.startsWith(viewportPrefix)) {
        event.preventDefault();
        const request = JSON.parse(title.slice(viewportPrefix.length)) as { requestId?: unknown; width?: unknown; height?: unknown };
        if (typeof request.requestId !== 'string' || !Number.isInteger(request.width) || !Number.isInteger(request.height) || Number(request.width) < 500 || Number(request.height) < 500) throw new Error('invalid production viewport request');
        window.setContentSize(Number(request.width), Number(request.height));
        window.webContents.executeJavaScript(`window.__resolveProductionViewport?.(${JSON.stringify({ requestId: request.requestId })})`);
        return;
      }
      const statusPrefix = 'VOID_PRODUCTION_STATUS_REQUEST:';
      if (productionProbeOutput && title.startsWith(statusPrefix)) {
        event.preventDefault();
        const request = sessionRequest(JSON.parse(title.slice(statusPrefix.length)));
        window.webContents.send(IPC.lifecycle, { sessionId: request.sessionId, state: 'working', unread: false, diagnostic: 'production focus regression' });
        return;
      }
      if (!title.startsWith(headless.prefix)) return;
      event.preventDefault(); const result = JSON.parse(title.slice(headless.prefix.length)) as { ok: boolean };
      writeFileSync(headless.output, `${JSON.stringify(result, null, 2)}
`, { mode: 0o600 }); manager.teardownOwner(ownerId); app.exit(result.ok ? 0 : 1);
    });
    const query = headless.query ? { ...headless.query, ...(productionProbePerturb ? { productionTerminalPerturb: productionProbePerturb } : {}) } : undefined;
    await startupStage('renderer-load', () => loadRenderer(() => window.loadFile(path.join(__dirname, `../renderer/${headless.page}`), query ? { query } : undefined)));
  } else await startupStage('renderer-load', () => loadAndPresentWindow(window, () => loadRenderer(() => window.loadFile(path.join(__dirname, `../renderer/${rendererFilename(missingRendererTest)}`)))));
  return window;
}

async function bootstrap(): Promise<void> {
  await startupStage('readiness', () => app.whenReady());
  runtime = await startupStage('runtime-validation', () => resolvePrivateRuntime(runtimeRoot));
  workspace = new WorkspaceStore(path.join(app.getPath('userData'), 'workspace.json'));
  if (productionProbeRoot) { workspace.setFolder(productionProbeRoot); workspace.newChat(randomUUID()); }
  const statusChannels = new StatusChannelStore(
    path.join(app.getPath('userData'), 'status-channels'),
    (ownerId, event) => manager?.lifecycleChanged(ownerId, event),
    (chatId) => workspace.view().workspace?.selectedId === chatId,
  );
  manager = new SessionManager((request, authority) => spawnRequest(runtime, request, authority), (ownerId, channel, payload) => webContents.fromId(ownerId)?.send(channel, payload), statusChannels);
  registerIpc(); await createWindow();
}

function failStartup(failure: StartupStageError): void {
  const userData = app.getPath('userData');
  try { writeStartupDiagnostic(userData, startupDiagnostic(failure.stage, failure.original, app.getVersion())); } catch { /* the native error remains available if durable storage fails */ }
  try {
    if (!process.argv.includes('--void-startup-test-no-dialog')) dialog.showErrorBox(
      'Void Code could not start',
      startupDialogMessage(),
    );
  } catch { /* startup still terminates if the native error cannot be presented */ }
  try { manager?.teardownAll(); } catch { /* startup still terminates if cleanup reports an error */ }
  app.exit(1);
}

if (!app.requestSingleInstanceLock()) app.exit(0);
else {
  app.on('second-instance', () => focusExistingWindow(mainWindow));
  void runBootstrap(bootstrap, failStartup);
}
app.on('before-quit', () => { manager?.teardownAll(); if (productionProbeRoot) rmSync(productionProbeRoot, { recursive: true, force: true }); });
app.on('window-all-closed', () => app.quit());

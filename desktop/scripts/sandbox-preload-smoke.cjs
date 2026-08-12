const { spawnSync } = require('node:child_process');
const { mkdtempSync, writeFileSync, rmSync } = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { clearTimeout } = require('node:timers');

if (!process.versions.electron) {
  const electron = require('electron');
  const environment = { ...process.env };
  delete environment.ELECTRON_RUN_AS_NODE;
  const arguments_ = process.platform === 'linux'
    ? ['--ozone-platform=headless', '--disable-gpu', __filename]
    : [__filename];
  const result = spawnSync(electron, arguments_, { env: environment, stdio: 'inherit' });
  if (result.error) throw result.error;
  process.exit(result.status ?? 1);
}

const { app, BrowserWindow, ipcMain } = require('electron');
const workspaceLoad = 'workspace:load';

void app.whenReady().then(async () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'void-code-sandbox-preload-'));
  const trustedPage = path.join(root, 'index.html');
  const untrustedPage = path.join(root, 'untrusted.html');
  const pageScript = path.join(root, 'page.js');
  writeFileSync(trustedPage, '<!doctype html><title>loading</title><script src="page.js"></script>', 'utf8');
  writeFileSync(untrustedPage, '<!doctype html><title>loading</title><script src="page.js"></script>', 'utf8');
  writeFileSync(pageScript, "window.voidTerminal.workspace.load().then(() => document.title = 'IPC:ACCEPTED').catch(() => document.title = 'IPC:REJECTED')", 'utf8');
  let activeWindow;
  let preloadError;
  const { rendererAuthority, rendererUrl } = require('../dist/main/renderer-authority.js');
  ipcMain.handle(workspaceLoad, (event) => {
    if (!activeWindow) throw new Error('renderer authority rejected');
    rendererAuthority(activeWindow.webContents, rendererUrl(trustedPage))(event);
    return { workspace: null, chats: [] };
  });
  const createWindow = () => {
    const window = new BrowserWindow({
      show: false,
      webPreferences: {
        preload: path.resolve(__dirname, '../dist/preload/index.js'),
        contextIsolation: true,
        nodeIntegration: false,
        sandbox: true,
      },
    });
    window.webContents.on('preload-error', (_event, preloadPath, error) => {
      preloadError = `${preloadPath}: ${error instanceof Error ? error.stack : String(error)}`;
    });
    return window;
  };
  const waitForResult = (window) => new Promise((resolve, reject) => {
    const timeout = setTimeout(() => reject(new Error('initial workspace IPC timed out')), 5000);
    window.webContents.on('page-title-updated', (_event, title) => {
      if (!title.startsWith('IPC:')) return;
      clearTimeout(timeout);
      resolve(title);
    });
  });
  const trusted = createWindow();
  const untrusted = createWindow();
  try {
    // Match production ordering: the newly created exact window becomes authoritative before load starts.
    activeWindow = trusted;
    const trustedResult = waitForResult(trusted);
    await trusted.loadFile(trustedPage);
    if (await trustedResult !== 'IPC:ACCEPTED') throw new Error('trusted initial workspace IPC was rejected');

    const untrustedResult = waitForResult(untrusted);
    await untrusted.loadFile(untrustedPage);
    if (await untrustedResult !== 'IPC:REJECTED') throw new Error('untrusted renderer path was accepted');
    if (preloadError) throw new Error(`sandbox preload error: ${preloadError}`);
    console.log('sandbox preload smoke: initial privileged IPC accepted for exact active window; untrusted path rejected');
    app.exit(0);
  } catch (error) {
    console.error(error instanceof Error ? error.stack : error);
    app.exit(1);
  } finally {
    ipcMain.removeHandler(workspaceLoad);
    trusted.destroy();
    untrusted.destroy();
    rmSync(root, { recursive: true, force: true });
  }
});

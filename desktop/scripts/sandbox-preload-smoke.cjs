const { spawnSync } = require('node:child_process');
const { mkdtempSync, writeFileSync, rmSync } = require('node:fs');
const os = require('node:os');
const path = require('node:path');

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

const { app, BrowserWindow } = require('electron');

void app.whenReady().then(async () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'void-code-sandbox-preload-'));
  const page = path.join(root, 'index.html');
  writeFileSync(page, '<!doctype html><title>sandbox preload smoke</title>', 'utf8');
  let preloadError;
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
  try {
    await window.loadFile(page);
    const bridgePresent = await window.webContents.executeJavaScript(
      "typeof window.voidTerminal === 'object' && typeof window.voidTerminal.workspace?.load === 'function'",
    );
    if (preloadError) throw new Error(`sandbox preload error: ${preloadError}`);
    if (!bridgePresent) throw new Error('sandbox preload did not expose window.voidTerminal');
    console.log('sandbox preload smoke: bridge exposed without preload errors');
    app.exit(0);
  } catch (error) {
    console.error(error instanceof Error ? error.stack : error);
    app.exit(1);
  } finally {
    window.destroy();
    rmSync(root, { recursive: true, force: true });
  }
});

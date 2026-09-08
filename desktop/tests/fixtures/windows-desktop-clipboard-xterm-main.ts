import { app, BrowserWindow } from 'electron';
import { writeFileSync } from 'node:fs';

function argument(name: string): string {
  const prefix = `--${name}=`;
  const value = process.argv.find((item) => item.startsWith(prefix))?.slice(prefix.length);
  if (!value) throw new Error(`missing ${name}`);
  return value;
}

const page = argument('fixture-page');
const resultFile = argument('fixture-result');

function key(window: BrowserWindow, type: 'keyDown' | 'keyUp', keyCode: 'C' | 'V'): void {
  window.webContents.sendInputEvent({ type, keyCode, modifiers: ['control'] });
}

void app.whenReady().then(async () => {
  const window = new BrowserWindow({
    show: false,
    width: 800,
    height: 400,
    webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true },
  });
  let stage: 'loading' | 'copy' | 'paste' | 'done' = 'loading';
  window.webContents.on('page-title-updated', (event, title) => {
    event.preventDefault();
    if (stage === 'loading' && title === 'XTERM:READY') {
      stage = 'copy';
      key(window, 'keyDown', 'C');
      key(window, 'keyUp', 'C');
      return;
    }
    if (stage === 'copy' && title === 'XTERM:COPY-DONE') {
      stage = 'paste';
      key(window, 'keyDown', 'V');
      return;
    }
    if (stage === 'paste' && title === 'XTERM:PASTE-DATA') {
      stage = 'done';
      key(window, 'keyUp', 'V');
      return;
    }
    if (stage === 'done' && title.startsWith('XTERM:RESULT:')) {
      writeFileSync(resultFile, title.slice('XTERM:RESULT:'.length), { encoding: 'utf8', mode: 0o600 });
      window.destroy();
      app.exit(0);
    }
  });
  window.webContents.on('render-process-gone', (_event, details) => {
    throw new Error(`renderer exited before fixture result: ${details.reason}`);
  });
  await window.loadFile(page);
});

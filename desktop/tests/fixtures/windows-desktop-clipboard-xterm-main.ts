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

function key(window: BrowserWindow, type: 'keyDown' | 'keyUp', keyCode: 'C' | 'V' | 'X', control = true): void {
  window.webContents.sendInputEvent({ type, keyCode, modifiers: control ? ['control'] : [] });
}

void app.whenReady().then(async () => {
  const window = new BrowserWindow({
    show: false,
    width: 800,
    height: 400,
    webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true },
  });
  let stage: 'loading' | 'copy' | 'paste' | 'type' | 'done' = 'loading';
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
      stage = 'type';
      key(window, 'keyUp', 'V');
      key(window, 'keyDown', 'X', false);
      return;
    }
    if (stage === 'type' && title === 'XTERM:TYPE-DATA') {
      stage = 'done';
      key(window, 'keyUp', 'X', false);
      return;
    }
    if (stage === 'done' && title.startsWith('XTERM:RESULT:')) {
      writeFileSync(resultFile, title.slice('XTERM:RESULT:'.length), { encoding: 'utf8', mode: 0o600 });
      // The result is on disk, so nothing is left to shut down cleanly. app.exit() and
      // process.exit() both run Chromium's shutdown, which on a loaded macOS runner can
      // sit idle for a minute after Node's exit hooks have run; the test then reports
      // "hidden Electron fixture did not finish" for a fixture that passed (#421).
      // reallyExit is Node's raw exit(3): exit code 0, no shutdown. The helper
      // processes follow their parent out.
      (process as unknown as { reallyExit(code: number): never }).reallyExit(0);
    }
  });
  window.webContents.on('render-process-gone', (_event, details) => {
    throw new Error(`renderer exited before fixture result: ${details.reason}`);
  });
  await window.loadFile(page);
});

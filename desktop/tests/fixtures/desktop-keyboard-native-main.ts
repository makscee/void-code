import { app, BrowserWindow } from 'electron';
import { writeFileSync } from 'node:fs';
import path from 'node:path';

const root = process.argv.find(value => value.startsWith('--fixture-root='))!.slice('--fixture-root='.length);
app.setPath('userData', path.join(root, 'userData'));
app.setPath('sessionData', path.join(root, 'sessionData'));
const watchdog = setTimeout(() => app.exit(2), 10_000);
let window: BrowserWindow;
const pause = (ms = 100) => new Promise(resolve => setTimeout(resolve, ms));
const snapshot = () => window.webContents.executeJavaScript(`({
  ...window.nativeKeysReceipt(),
  selected: document.querySelector('#tabs .selected .tab-title')?.textContent || document.querySelector('#tabs .selected')?.textContent,
  focus: document.activeElement?.matches('.terminal:not([hidden]) .xterm-helper-textarea'),
  terminals: document.querySelectorAll('.terminal .xterm-helper-textarea').length
})`);
void app.whenReady().then(async () => {
  app.dock?.hide();
  window = new BrowserWindow({ show: false, width: 1000, height: 700, webPreferences: {
    preload: path.join(root, 'preload.cjs'), sandbox: true, contextIsolation: true, nodeIntegration: false,
  } });
  window.webContents.setWindowOpenHandler(() => ({ action: 'deny' }));
  window.webContents.session.webRequest.onBeforeRequest({ urls: ['http://*/*', 'https://*/*'] }, (_details, callback) => callback({ cancel: true }));
  await window.loadFile(path.join(root, 'index.html'));
  const deadline = Date.now() + 3000;
  while (!(await snapshot()).focus && Date.now() < deadline) await pause(25);
  await window.webContents.executeJavaScript(`window.nativeKeyEvents = []; for (const type of ['keydown', 'keyup']) window.addEventListener(type, event => { if (event.key === 'Tab') { const row = { type, trusted: event.isTrusted, ctrl: event.ctrlKey, shift: event.shiftKey, prevented: false }; window.nativeKeyEvents.push(row); setTimeout(() => { row.prevented = event.defaultPrevented; }, 0); } }, true);`);
  const initial = await snapshot();
  const steps = [];
  for (const shift of [false, true, true, false, false, false, false]) {
    const modifiers = shift ? ['control', 'shift'] : ['control'];
    window.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'Tab', modifiers });
    window.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'Tab', modifiers });
    await pause(180);
    const navigation = await snapshot();
    window.webContents.sendInputEvent({ type: 'keyDown', keyCode: 'X' });
    window.webContents.sendInputEvent({ type: 'char', keyCode: 'x' });
    window.webContents.sendInputEvent({ type: 'keyUp', keyCode: 'X' });
    await pause();
    steps.push({ navigation, typed: await snapshot() });
  }
  const events = await window.webContents.executeJavaScript('window.nativeKeyEvents');
  writeFileSync(path.join(root, 'result.json'), JSON.stringify({ initial, steps, events, electron: process.versions.electron, platform: process.platform }));
  window.destroy(); clearTimeout(watchdog); app.exit(0);
}).catch(error => { console.error(error); window?.destroy(); clearTimeout(watchdog); app.exit(1); });

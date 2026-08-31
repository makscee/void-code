import { BrowserWindow } from 'electron';
import path from 'node:path';
import { installNavigationPolicy } from './renderer-authority';
import type { SplashWindow } from './startup-lifecycle';

// A window of its own, deliberately not the main one: the main window is wrapped in ownership
// (ownerId, manager.teardownOwner, renderer authority) that a throwaway window must not enter.
// It shows immediately and paints its own background colour, so the person sees something even in
// the instants before the page itself is on screen.
export function createSplashWindow(): SplashWindow {
  const window = new BrowserWindow({
    title: 'Void Code', show: true, width: 460, height: 260, center: true, resizable: false,
    minimizable: false, maximizable: false, fullscreenable: false, skipTaskbar: true, frame: false,
    alwaysOnTop: true, backgroundColor: '#101216',
    webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true },
  });
  installNavigationPolicy(window.webContents);
  // Not awaited: the whole point is that startup does not wait on this window, and a page that
  // fails to load leaves the coloured window standing rather than taking startup down with it.
  void window.loadFile(path.join(__dirname, '../renderer/splash.html')).catch(() => undefined);
  return window;
}

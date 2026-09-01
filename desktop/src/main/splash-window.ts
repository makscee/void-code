import { BrowserWindow } from 'electron';
import type { BrowserWindowConstructorOptions } from 'electron';
import path from 'node:path';
import { installNavigationPolicy } from './renderer-authority';
import type { SplashWindow } from './startup-lifecycle';

/**
 * What kind of window a person is left in front of for the length of a cold start.
 *
 * A splash that floats above everything and keeps out of the taskbar is the usual look, and it is
 * wrong here: a cold Windows start reaches 331 seconds, `resolvePrivateRuntime` has no timeout, and
 * a startup that never finishes leaves that window on top of the person's work with no way to reach
 * it. So it behaves like an ordinary window — it can be moved, minimised, listed and closed — and
 * closing it ends a hung start, because `window-all-closed` quits the app (src/main/index.ts:244) —
 * measured on a fixture whose startup hangs with only the splash up: the app exited with code 0.
 *
 * The frame stays: a title bar is the one dismissal that needs no knowledge of taskbar context
 * menus or the Dock, and it is the same gesture on both platforms we ship.
 */
export function splashWindowOptions(): BrowserWindowConstructorOptions {
  return {
    // Размер снят с содержимого, а не назначен: спиннер, название, строка состояния и подпись
    // укладываются в ~120 px, остальное — запас на другой системный шрифт (на Windows это Segoe UI,
    // а мерил я на macOS). Обе стороны обязаны остаться ниже mainWindowMinimumEdge
    // (scripts/window-thresholds.mjs), иначе перекличка окон начнёт мерить splash вместо главного.
    //
    // autoHideMenuBar, а не setMenu(null): опция живёт в этом же объекте — единственном месте, где
    // окно настраивается, — и не требует ветки по системам; setMenu есть только на Windows и Linux.
    title: 'Void Code', show: true, width: 360, height: 220, center: true, autoHideMenuBar: true,
    resizable: false, maximizable: false, fullscreenable: false, backgroundColor: '#101216',
    // backgroundThrottling: замерено на WIN11-VCLAB, а не выбрано по вкусу. Слежка Chromium за
    // перекрытием считала это окно закрытым и гасила документ целиком: visibilityState 'hidden'
    // одиннадцать выборок подряд при hasFocus true, страница разобрана и свёрстана — и ни одного
    // кадра. С этой опцией и без единого флага запуска — 'visible' четырнадцать выборок подряд и
    // весь текст на снимке.
    webPreferences: { contextIsolation: true, nodeIntegration: false, sandbox: true, backgroundThrottling: false },
  };
}

// A window of its own, deliberately not the main one: the main window is wrapped in ownership
// (ownerId, manager.teardownOwner, renderer authority) that a throwaway window must not enter.
// It shows immediately and paints its own background colour, so the person sees something even in
// the instants before the page itself is on screen.
export function createSplashWindow(): SplashWindow {
  const window = new BrowserWindow(splashWindowOptions());
  installNavigationPolicy(window.webContents);
  // Not awaited: the whole point is that startup does not wait on this window, and a page that
  // fails to load leaves the coloured window standing rather than taking startup down with it.
  void window.loadFile(path.join(__dirname, '../renderer/splash.html')).catch(() => undefined);
  return window;
}

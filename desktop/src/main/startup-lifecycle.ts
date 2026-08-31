import type { StartupStage } from './startup-diagnostic';

export class StartupStageError extends Error {
  constructor(public readonly stage: StartupStage, public readonly original: unknown) {
    super(`startup failed during ${stage}`);
    this.name = 'StartupStageError';
  }
}

export async function startupStage<T>(stage: StartupStage, action: () => T | Promise<T>): Promise<T> {
  try { return await action(); } catch (error) { throw new StartupStageError(stage, error); }
}

export async function runBootstrap(bootstrap: () => Promise<void>, failure: (error: StartupStageError) => void | Promise<void>): Promise<void> {
  try { await bootstrap(); } catch (error) {
    const staged = error instanceof StartupStageError ? error : new StartupStageError('readiness', error);
    await failure(staged);
  }
}

interface PresentableWindow {
  show(): void;
  focus(): void;
}

interface FocusableWindow extends PresentableWindow {
  isDestroyed(): boolean;
  isMinimized(): boolean;
  restore(): void;
}

export function missingRendererRequested(args: string[], environment: string | undefined, sentinelPresent = false): boolean {
  return args.includes('--void-startup-test-missing-renderer') || environment === '1' || sentinelPresent;
}

export function rendererFilename(missingRendererTest: boolean): string {
  return missingRendererTest ? 'missing-renderer-test.html' : 'index.html';
}

export async function loadRenderer<T>(load: () => Promise<T>, timeoutMs = 10_000): Promise<T> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      load(),
      new Promise<never>((_resolve, reject) => { timer = setTimeout(() => reject(new Error('renderer load timed out')), timeoutMs); }),
    ]);
  } finally { if (timer) clearTimeout(timer); }
}

export async function loadAndPresentWindow(window: PresentableWindow, load: () => Promise<unknown>): Promise<void> {
  await load();
  window.show();
  window.focus();
}

export function focusExistingWindow(window: FocusableWindow | undefined): void {
  if (!window || window.isDestroyed()) return;
  if (window.isMinimized()) window.restore();
  window.show();
  window.focus();
}

// The splash is a separate window from the main one, so it carries only what closing it needs.
export interface SplashWindow {
  close(): void;
  isDestroyed(): boolean;
}

// Cold start on Windows measured 331.7s (Defender inspecting 19068 freshly unpacked files), warm
// 13.3s, with an empty screen for all of it. Everything heavy runs inside `work`; the splash is
// opened before it and closed after it, on both the success and the failure path — a startup that
// dies behind a splash that never goes away is worse than the blank screen it replaced.
// Neither the splash failing to open nor failing to close may take the application down with it.
export async function withStartupSplash<T>(createSplash: (() => SplashWindow) | undefined, work: () => Promise<T>): Promise<T> {
  let splash: SplashWindow | undefined;
  const openSplash = () => { try { splash = createSplash?.(); } catch { /* no display, no splash — the application still starts */ } };
  const closeSplash = () => {
    if (!splash) return;
    const window = splash;
    splash = undefined;
    try { if (!window.isDestroyed()) window.close(); } catch { /* the startup outcome is what matters, not the splash's exit */ }
  };
  openSplash();
  let result: T;
  try { result = await work(); } catch (error) { closeSplash(); throw error; }
  closeSplash();
  return result;
}

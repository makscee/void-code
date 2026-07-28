import { statSync } from 'node:fs';
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

export function requireRendererFile(file: string, inspect: (candidate: string) => { isFile(): boolean } = statSync): string {
  if (!inspect(file).isFile()) throw new Error('renderer is not a file');
  return file;
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

import { describe, expect, it, vi } from 'vitest';
import { focusExistingWindow, runBootstrap, startupStage } from '../src/main/startup-lifecycle';

describe('startup lifecycle', () => {
  it('routes asynchronous stage rejection through one failure handler', async () => {
    const failure = vi.fn(async () => undefined);
    await runBootstrap(async () => startupStage('renderer-load', async () => { throw new Error('renderer failed to load'); }), failure);
    expect(failure).toHaveBeenCalledOnce();
    expect(failure.mock.calls[0][0]).toMatchObject({ stage: 'renderer-load' });
  });

  it('restores, shows, and focuses the existing window for a second instance', () => {
    const window = { isDestroyed: vi.fn(() => false), isMinimized: vi.fn(() => true), restore: vi.fn(), show: vi.fn(), focus: vi.fn() };
    focusExistingWindow(window);
    expect(window.restore).toHaveBeenCalledOnce();
    expect(window.show).toHaveBeenCalledOnce();
    expect(window.focus).toHaveBeenCalledOnce();
  });
});

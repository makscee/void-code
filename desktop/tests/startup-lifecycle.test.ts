import { describe, expect, it, vi } from 'vitest';
import { focusExistingWindow, loadAndPresentWindow, requireRendererFile, runBootstrap, startupStage } from '../src/main/startup-lifecycle';

describe('startup lifecycle', () => {
  it('routes asynchronous stage rejection through one failure handler', async () => {
    const failure = vi.fn(async () => undefined);
    await runBootstrap(async () => startupStage('renderer-load', async () => { throw new Error('renderer failed to load'); }), failure);
    expect(failure).toHaveBeenCalledOnce();
    expect(failure.mock.calls[0][0]).toMatchObject({ stage: 'renderer-load' });
  });

  it('rejects a missing renderer before Electron can hang on load', () => {
    const inspect = vi.fn(() => { throw Object.assign(new Error('missing'), { code: 'ENOENT' }); });
    expect(() => requireRendererFile('renderer/index.html', inspect)).toThrow('missing');
    expect(inspect).toHaveBeenCalledWith('renderer/index.html');
  });

  it('rejects a renderer path that is not a regular file', () => {
    expect(() => requireRendererFile('renderer/index.html', () => ({ isFile: () => false }))).toThrow('renderer is not a file');
  });

  it('presents a newly loaded window only after renderer load completes', async () => {
    const order: string[] = [];
    const window = { show: vi.fn(() => order.push('show')), focus: vi.fn(() => order.push('focus')) };
    await loadAndPresentWindow(window, async () => { order.push('load'); });
    expect(order).toEqual(['load', 'show', 'focus']);
  });

  it('does not present a new window when renderer load fails', async () => {
    const window = { show: vi.fn(), focus: vi.fn() };
    await expect(loadAndPresentWindow(window, async () => { throw new Error('load failed'); })).rejects.toThrow('load failed');
    expect(window.show).not.toHaveBeenCalled();
    expect(window.focus).not.toHaveBeenCalled();
  });

  it('restores, shows, and focuses the existing window for a second instance', () => {
    const window = { isDestroyed: vi.fn(() => false), isMinimized: vi.fn(() => true), restore: vi.fn(), show: vi.fn(), focus: vi.fn() };
    focusExistingWindow(window);
    expect(window.restore).toHaveBeenCalledOnce();
    expect(window.show).toHaveBeenCalledOnce();
    expect(window.focus).toHaveBeenCalledOnce();
  });
});

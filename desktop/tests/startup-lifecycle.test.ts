import { describe, expect, it, vi } from 'vitest';
import { focusExistingWindow, loadAndPresentWindow, loadRenderer, missingRendererRequested, rendererFilename, runBootstrap, startSingleWindow, startupStage } from '../src/main/startup-lifecycle';

describe('startup lifecycle', () => {
  it('routes asynchronous stage rejection through one failure handler', async () => {
    const failure = vi.fn(async () => undefined);
    await runBootstrap(async () => startupStage('renderer-load', async () => { throw new Error('renderer failed to load'); }), failure);
    expect(failure).toHaveBeenCalledOnce();
    expect(failure.mock.calls[0][0]).toMatchObject({ stage: 'renderer-load' });
  });

  it('accepts the missing-renderer test hook through argv or the packaged-check environment', () => {
    expect(missingRendererRequested(['--void-startup-test-missing-renderer'], undefined)).toBe(true);
    expect(missingRendererRequested([], '1')).toBe(true);
    expect(missingRendererRequested([], undefined, true)).toBe(true);
    expect(missingRendererRequested([], undefined)).toBe(false);
  });

  it('selects a genuinely absent renderer only for the explicit startup test hook', () => {
    expect(rendererFilename(false)).toBe('index.html');
    expect(rendererFilename(true)).toBe('missing-renderer-test.html');
  });

  it('bounds a renderer load that never settles', async () => {
    vi.useFakeTimers();
    try {
      const result = expect(loadRenderer(() => new Promise(() => undefined), 10)).rejects.toThrow('renderer load timed out');
      await vi.advanceTimersByTimeAsync(10);
      await result;
    } finally { vi.useRealTimers(); }
  });

  it('accepts a renderer load that settles within the bound', async () => {
    await expect(loadRenderer(async () => 'loaded', 10)).resolves.toBe('loaded');
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

describe('one window, loading page first', () => {
  // Artem's decision of 01.09: the loader gets the same size as the app, and at the same size two
  // windows stop making sense. So there is one window, it opens on the loading page, and its
  // contents are swapped for the application once the heavy part is done.
  //
  // The obstacle that makes this an ordering problem rather than a one-liner: createWindow()
  // attaches `did-start-navigation -> manager.teardownOwner(ownerId)`, and `manager` is assigned
  // only after runtime validation. An ownership handler attached before the loading page loads
  // fires on that page's own navigation, with `manager` still undefined, and throws inside the
  // listener. The fakes below reproduce exactly that: loading a page runs the registered
  // navigation listeners, and the listener reaches for a manager that preparation creates.

  const startup = () => {
    const order: string[] = [];
    const navigation: Array<() => void> = [];
    let manager: { teardownOwner: () => void } | undefined;
    const window = {
      loadLoadingPage: vi.fn(async () => { order.push('loading page'); for (const listener of navigation) listener(); }),
      loadApplicationPage: vi.fn(async () => { order.push('application page'); for (const listener of navigation) listener(); }),
    };
    return {
      order,
      window,
      openWindow: async () => { order.push('window'); return window; },
      prepare: async () => { order.push('prepare'); manager = { teardownOwner: () => order.push('teardown') }; return manager; },
      takeOwnership: () => {
        order.push('ownership');
        navigation.push(() => {
          if (manager === undefined) throw new Error('did-start-navigation reached an undefined manager');
          manager.teardownOwner();
        });
      },
    };
  };

  it('does not let the loading page navigate into ownership that nothing has wired up yet', async () => {
    const { openWindow, prepare, takeOwnership } = startup();
    // The assertion is the absence of that throw. Attach ownership before the loading page and this
    // rejects with the same error the real app would raise inside its own listener.
    await expect(startSingleWindow(openWindow, prepare, takeOwnership)).resolves.toBeDefined();
  });

  it('opens on the loading page before the heavy work and swaps to the application only after it', async () => {
    const { order, window, openWindow, prepare, takeOwnership } = startup();
    const returned = await startSingleWindow(openWindow, prepare, takeOwnership);
    expect(order).toEqual(['window', 'loading page', 'prepare', 'ownership', 'application page', 'teardown']);
    expect(window.loadLoadingPage).toHaveBeenCalledOnce();
    expect(window.loadApplicationPage).toHaveBeenCalledOnce();
    expect(returned, 'the caller cannot reach the window it is meant to keep').toBe(window);
  });
});

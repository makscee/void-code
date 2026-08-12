import { describe, expect, it, vi } from 'vitest';
import { installNavigationPolicy, rendererAuthority, rendererUrl } from '../src/main/renderer-authority';

describe('renderer authority', () => {
  const expected = rendererUrl('/app/renderer/index.html');
  function fixture(url = expected) { const frame = { url }; const contents = { mainFrame: frame }; return { contents, frame, event: { sender: contents, senderFrame: frame } }; }
  it('accepts only the exact active main frame', () => {
    const valid = fixture(); const guard = rendererAuthority(valid.contents, expected);
    expect(() => guard(valid.event)).not.toThrow();
    expect(() => guard(fixture().event)).toThrow();
    expect(() => guard({ sender: valid.contents, senderFrame: { url: expected } })).toThrow();
  });
  it.each(['https://example.test/', rendererUrl('/app/renderer/smoke.html'), `${expected}?probe=1`, `${expected}#fragment`, 'file:///app/renderer/%2e%2e/secret.html'])('rejects %s', (url) => {
    const valid = fixture(url); expect(() => rendererAuthority(valid.contents, expected)(valid.event)).toThrow();
  });
  it('accepts only explicitly selected query mode', () => {
    const probe = rendererUrl('/app/renderer/index.html', { productionTerminalProbe: '1' }); const valid = fixture(probe);
    expect(() => rendererAuthority(valid.contents, probe)(valid.event)).not.toThrow();
    expect(() => rendererAuthority(valid.contents, expected)(valid.event)).toThrow();
  });
  it('installs denial policies for navigation, frames, popups, and webviews', () => {
    const listeners = new Map<string, (event: { preventDefault(): void }) => void>(); let popup: (() => { action: 'deny' }) | undefined;
    installNavigationPolicy({ on: (name, listener) => listeners.set(name, listener), setWindowOpenHandler: (handler) => { popup = handler; } });
    for (const name of ['will-navigate', 'will-frame-navigate', 'will-attach-webview']) { const preventDefault = vi.fn(); listeners.get(name)?.({ preventDefault }); expect(preventDefault).toHaveBeenCalledOnce(); }
    expect(popup?.()).toEqual({ action: 'deny' });
  });
});

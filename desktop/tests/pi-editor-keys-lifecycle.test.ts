import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { attach, editorRig, keys, keysModule, type EditorRig } from './fixtures/pi-editor-keys';

// Input-time priority contracts, using Pi's real editor and setupKeyHandlers.
// Native controls do not install the adapter; managed cases intentionally stay RED.
const live: EditorRig[] = [];
beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(10_000);
  vi.stubGlobal('fetch', vi.fn(() => { throw new Error('offline only'); }));
});
afterEach(() => {
  live.reverse().forEach(r => r.close());
  live.length = 0;
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
});
async function make() {
  const r = await editorRig();
  live.push(r);
  r.draft('original draft 世界\nsecond line');
  expect(r.editor.onEscape).toBeTypeOf('function');
  return r;
}

for (const managed of [false, true]) describe(managed ? 'managed RED' : 'native control', () => {
  it('K2/K6: temporary callback at install retains priority, then restored native Esc clears without reinstall', async () => {
    const r = await make();
    const draft = r.editor.getText();
    const ordinary = r.editor.onEscape;
    const temporary = vi.fn();
    r.editor.onEscape = temporary;
    if (managed) attach(await keysModule(), r);
    r.input(keys.esc);
    expect(temporary).toHaveBeenCalledTimes(1);
    expect(r.editor.onEscape).toBe(temporary);
    expect.soft(r.editor.getText(), 'temporary callback owns the installing lifecycle').toBe(draft);
    r.editor.onEscape = ordinary;
    vi.advanceTimersByTime(1_000);
    r.input(keys.esc);
    expect(r.editor.onEscape).toBe(ordinary);
    expect(temporary).toHaveBeenCalledTimes(1);
    expect(r.editor.getText(), 'restoration is observed without adapter reload').toBe(managed ? '' : draft);
  });

  it('K2/K6: synchronous callback restoration cannot clear the restoring Esc', async () => {
    const r = await make();
    const draft = r.editor.getText();
    const ordinary = r.editor.onEscape;
    if (managed) attach(await keysModule(), r);
    const temporary = vi.fn(() => { r.editor.onEscape = ordinary; });
    r.editor.onEscape = temporary;
    r.input(keys.esc);
    expect(temporary).toHaveBeenCalledTimes(1);
    expect(r.editor.onEscape).toBe(ordinary);
    expect.soft(r.editor.getText(), 'priority belongs to callback active at input time').toBe(draft);
    vi.advanceTimersByTime(1_000);
    r.input(keys.esc);
    expect(temporary).toHaveBeenCalledTimes(1);
    expect(r.editor.getText()).toBe(managed ? '' : draft);
  });

  it('K2/K6: native busy cancellation becoming idle cannot also clear the original draft', async () => {
    const r = await make();
    const draft = r.editor.getText();
    const ordinary = r.editor.onEscape;
    r.activity.idle = false;
    r.session.isStreaming = true;
    r.receiver.restoreQueuedMessagesToEditor.mockImplementation(() => {
      expect(r.editor.getText()).toBe(draft);
      r.session.isStreaming = false;
      r.activity.idle = true;
    });
    if (managed) attach(await keysModule(), r);
    r.input(keys.esc);
    expect(r.receiver.restoreQueuedMessagesToEditor).toHaveBeenCalledExactlyOnceWith({ abort: true });
    expect(r.editor.onEscape).toBe(ordinary);
    expect(r.ctx.isIdle()).toBe(true);
    expect.soft(r.editor.getText(), 'busy at input time: cancellation is the sole Esc action').toBe(draft);
    vi.advanceTimersByTime(1_000);
    r.input(keys.esc);
    expect(r.receiver.restoreQueuedMessagesToEditor).toHaveBeenCalledTimes(1);
    expect(r.editor.getText()).toBe(managed ? '' : draft);
    expect(r.submit).not.toHaveBeenCalled();
    expect(r.receiver.handleCtrlC).not.toHaveBeenCalled();
    expect(r.receiver.handleCtrlD).not.toHaveBeenCalled();
  });
});

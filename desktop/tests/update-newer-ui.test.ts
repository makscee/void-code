import { describe, expect, it } from 'vitest';
import { renderUpdateStatus } from '../src/renderer/update-view';

describe('About update UI', () => {
  it('shows a newer accepted version and exact Update now action', () => {
    const elements = { current: { textContent: '' }, status: { textContent: '' }, action: { textContent: '', hidden: true } };
    renderUpdateStatus(elements, { state: 'available', currentVersion: '0.1.1', availableVersion: '0.2.0', canRetry: false });
    expect(elements.current.textContent).toBe('Current version 0.1.1'); expect(elements.status.textContent).toBe('Version 0.2.0 is available');
    expect(elements.action).toEqual({ textContent: 'Update now', hidden: false });
  });
  it('shows progress bytes and a retry action after failure', () => {
    const elements = { current: { textContent: '' }, status: { textContent: '' }, action: { textContent: '', hidden: true } };
    renderUpdateStatus(elements, { state: 'downloading', currentVersion: '0.1.1', availableVersion: '0.2.0', percent: 50, transferred: 21, total: 42, canRetry: false });
    expect(elements.status.textContent).toContain('50% (21 of 42 bytes)'); expect(elements.action.hidden).toBe(true);
    renderUpdateStatus(elements, { state: 'failed', currentVersion: '0.1.1', availableVersion: '0.2.0', canRetry: true });
    expect(elements.status.textContent).toContain('retry'); expect(elements.action).toEqual({ textContent: 'Retry update', hidden: false });
  });
});

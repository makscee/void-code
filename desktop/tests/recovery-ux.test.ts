import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { RECOVERY_GUIDANCE } from '../src/renderer/recovery';

describe('stable pilot recovery guidance', () => {
  it('covers every required coarse recovery state with an action', () => {
    // A person can now sign in from inside the app, so the guidance must stop sending them to
    // an operator and a terminal for the common case — that's the wall this step removes.
    expect(RECOVERY_GUIDANCE.AUTH_PREFLIGHT_REQUIRED.detail).toMatch(/sign in.*directly.*network/i);
    expect(RECOVERY_GUIDANCE.SESSION_START_FAILED.detail).toMatch(/sign-in.*network.*Restart.*Support Report/i);
    expect(RECOVERY_GUIDANCE.RUNTIME_EXITED.detail).toMatch(/Restart.*no shell.*Support Report/i);
    expect(RECOVERY_GUIDANCE.WORKSPACE_MISSING.detail).toMatch(/Locate.*remove.*fallback folder/i);
    expect(RECOVERY_GUIDANCE.SESSION_MISSING.detail).toMatch(/Close.*start a new one.*not removed/i);
    expect(RECOVERY_GUIDANCE.SESSION_START_FAILED.canRestart).toBe(true);
    expect(RECOVERY_GUIDANCE.RUNTIME_EXITED.canRestart).toBe(true);
    expect(RECOVERY_GUIDANCE.WORKSPACE_MISSING.canRestart).toBe(false);
  });

  it('does not expose raw errors or paths in recovery presentation', () => {
    const renderer = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');
    const html = readFileSync(new URL('../src/renderer/index.html', import.meta.url), 'utf8');
    expect(renderer).not.toContain('endedDetail.textContent = message');
    expect(renderer).not.toContain('exit ${exitCode}');
    expect(renderer).not.toContain('signal ${signal}');
    expect(renderer).not.toContain('error instanceof Error ? error.message : String(error)');
    expect(renderer).not.toContain('recoveryPathElement.textContent = view.recoveryPath');
    expect(renderer).toContain("else if (selected?.exited)");
    expect(renderer).toContain("showEnded(code === 'SESSION_START_FAILED' || code === 'SESSION_MISSING' || code === 'RUNTIME_EXITED'");
    expect(renderer).toContain("folderElement.textContent = recovering ? 'Workspace unavailable'");
    expect(html).toContain('The previously selected folder cannot be found.');
    expect(html).toContain('It excludes sign-in data, paths, file or chat content, terminal output and environment variables.');
  });
});

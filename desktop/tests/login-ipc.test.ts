import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('login IPC allowlist', () => {
  it('exposes only zero-payload login operations through preload', () => {
    const preload = readFileSync(new URL('../src/preload/index.ts', import.meta.url), 'utf8');
    expect(preload).toContain('start: () => ipcRenderer.invoke(IPC.loginStart)');
    expect(preload).toContain('cancel: () => ipcRenderer.invoke(IPC.loginCancel)');
    expect(preload).toContain('status: () => ipcRenderer.invoke(IPC.loginStatus)');
    expect(preload).toContain('listener(loginStatus(payload))');
    expect(preload).not.toMatch(/loginStart,\s*(?:request|payload|args|url|token)/);
  });

  it('rejects extra renderer arguments and binds the package-validated VC path', () => {
    const main = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');
    expect(main).toContain("if (args.length !== 0) throw new Error('login does not accept arguments')");
    expect(main).toContain('new LoginManager(runtime.vc, runtime.root)');
    expect(main).toContain('loginManager?.shutdown()');
    expect(main).not.toMatch(/LoginManager\([^)]*(?:which|where|shell|command)/);
  });
});

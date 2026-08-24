import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// index.ts can't be imported directly in a test (it side-effects on import: app.requestSingleInstanceLock,
// app.whenReady, etc. — the same reason ipc-authority.test.ts parses it as text instead of running it).
// These checks close the two holes a lazy implementation could hide behind a passing
// ipc-authority.test.ts: a spawner that quietly falls back to a PATH-resolved `vc`, and a status
// handler that returns a hardcoded value instead of actually running vc through the shipped binary.
const source = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');

describe('auth IPC handlers use the shipped vc binary, not a hardcoded or PATH-resolved one', () => {
  it('imports the production auth spawner and auth-session functions', () => {
    expect(source).toMatch(/from ['"]\.\/auth-spawn['"]/);
    expect(source).toContain('spawnAuthProcess');
    expect(source).toMatch(/from ['"]\.\/auth-session['"]/);
    expect(source).toContain('readAuthStatus');
  });

  it('reads auth status via readAuthStatus with the runtime-resolved vc path, not a bare "vc"', () => {
    expect(source).toMatch(/readAuthStatus\(\s*runtime\.vc\s*,\s*spawnAuthProcess/);
  });

  it('starts login via the runtime-resolved vc path, not a bare "vc"', () => {
    expect(source).toMatch(/startAuthLogin\([^)]*runtime\.vc[^)]*spawnAuthProcess/s);
  });

  it('never spawns the auth process by the bare command name "vc"', () => {
    expect(source).not.toMatch(/spawn(?:AuthProcess)?\(\s*['"]vc['"]/);
  });
});

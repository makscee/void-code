import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

// index.ts side-effects on import (app.requestSingleInstanceLock, app.whenReady, ...), same
// reason auth-index-wiring.test.ts and ipc-authority.test.ts parse it as text instead of
// running it. This file pins the one wire that was missing: startAuthLogin's onDiagnostic
// parameter exists (auth-ipc.ts) but nothing in index.ts ever supplied it, so a real login
// failure's stderr text ("device authorization rate limited") had nowhere to land and vanished.
const source = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');

describe('login diagnostics are captured, not thrown away', () => {
  it('imports the bounded diagnostics store', () => {
    expect(source).toMatch(/from ['"]\.\/auth-diagnostics['"]/);
    expect(source).toContain('createLoginDiagnosticsStore');
  });

  it('creates the store once at module scope, not per login attempt', () => {
    // A store rebuilt inside the auth:login-start handler would forget every earlier attempt
    // the moment a second login starts — retention across attempts is the whole point.
    const handlerBody = source.slice(source.indexOf('function registerIpc'));
    expect(handlerBody).not.toMatch(/createLoginDiagnosticsStore\(/);
    expect(source).toMatch(/const\s+loginDiagnostics\s*=\s*createLoginDiagnosticsStore\(/);
  });

  it('passes an onDiagnostic to startAuthLogin that records into that store under the loginId', () => {
    // The call is written on one line in index.ts (same style as its neighbours), so grabbing
    // that whole line sidesteps having to balance the nested nested-arrow parens with a regex.
    const callLine = source.split('\n').find((line) => line.includes('startAuthLogin(loginId'));
    expect(callLine, 'expected a startAuthLogin(loginId, ...) call in index.ts').toBeDefined();
    expect(callLine).toMatch(/^\s*startAuthLogin\(\s*loginId,\s*runtime\.vc,\s*spawnAuthProcess,/);
    expect(callLine).toMatch(/\(\s*message\s*\)\s*=>\s*loginDiagnostics\.record\(\s*loginId\s*,\s*message\s*\)/);
  });

  it('never wires the diagnostics store into the deliver callback that reaches the renderer', () => {
    // The whole point of onDiagnostic being a separate parameter from deliver (auth-ipc.ts,
    // auth-session.ts) is that diagnostic text stays off the channel a window renders as UI
    // text. Routing it into the same webContents.send call here would quietly undo that.
    const callLine = source.split('\n').find((line) => line.includes('startAuthLogin(loginId'));
    expect(callLine).toBeDefined();
    const deliverArgument = callLine!.split('loginDiagnostics.record')[0];
    expect(deliverArgument).not.toContain('loginDiagnostics');
  });
});

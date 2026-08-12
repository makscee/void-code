import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { IPC } from '../src/shared/preload-contract';

describe('IPC authority wiring', () => {
  it('guards every renderer request channel before effects', () => {
    const source = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');
    const requestChannels = Object.entries(IPC).filter(([name]) => !['output', 'exit', 'lifecycle'].includes(name));
    for (const [, channel] of requestChannels) {
      const registration = source.indexOf(`ipcMain.${channel === IPC.subscribe || channel === IPC.unsubscribe ? 'on' : 'handle'}(IPC.${Object.entries(IPC).find(([, value]) => value === channel)?.[0]}`);
      expect(registration, `${channel} is registered`).toBeGreaterThan(-1);
      const handlerPrefix = source.slice(registration, registration + 180);
      expect(handlerPrefix.indexOf('assertRenderer(event)'), `${channel} invokes authority`).toBeGreaterThan(-1);
    }
  });
});

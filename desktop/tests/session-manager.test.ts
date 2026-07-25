import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { SessionManager } from '../src/main/session-manager';
import { StatusChannelStore } from '../src/main/status-channel';
import type { TerminalProcess } from '../src/main/session-manager';

class FakeProcess implements TerminalProcess {
  writes: string[] = []; sizes: number[][] = []; killed = false;
  private data = new Set<(value: string) => void>();
  private exits = new Set<(value: { exitCode: number }) => void>();
  write(value: string): void { this.writes.push(value); }
  resize(cols: number, rows: number): void { this.sizes.push([cols, rows]); }
  kill(): void { this.killed = true; }
  onData(listener: (data: string) => void): { dispose(): void } { this.data.add(listener); return { dispose: () => { this.data.delete(listener); } }; }
  onExit(listener: (event: { exitCode: number }) => void): { dispose(): void } { this.exits.add(listener); return { dispose: () => { this.exits.delete(listener); } }; }
  emitData(value: string): void { for (const listener of this.data) listener(value); }
  emitExit(code: number): void { for (const listener of this.exits) listener({ exitCode: code }); }
}
const subscription = (sessionId: string, kind: 'output' | 'exit' | 'status', id: string) => ({ sessionId, kind, subscriptionId: id });
const start = (sessionId: string) => ({ sessionId, fixture: 'roundTrip' as const });

describe('renderer-owned terminal sessions', () => {
  it('routes owned input, resize, status, output and exit', () => {
    const process = new FakeProcess(); const delivered: unknown[] = [];
    const manager = new SessionManager(() => process, (_owner, channel, payload) => delivered.push([channel, payload]));
    expect(manager.start(1, start('one'))).toEqual({ status: 'running', showSharedFilesWarning: false });
    manager.subscribe(1, subscription('one', 'output', 'a'));
    manager.subscribe(1, subscription('one', 'exit', 'b'));
    manager.input(1, 'one', 'hello'); manager.resize(1, 'one', 90, 20);
    process.emitData('world'); process.emitExit(0);
    expect(process.writes).toEqual(['hello']); expect(process.sizes).toEqual([[90, 20]]);
    expect(manager.status(1, 'one')).toBe('exited');
    expect(delivered).toEqual([['terminal:output', { sessionId: 'one', data: 'world' }], ['terminal:exit', { sessionId: 'one', exitCode: 0 }]]);
  });
  it('rejects cross-renderer access, unknown sessions and duplicate ownership', () => {
    const manager = new SessionManager(() => new FakeProcess(), () => undefined);
    manager.start(1, start('one'));
    expect(() => manager.start(2, start('one'))).toThrow('already owned');
    expect(() => manager.input(2, 'one', 'steal')).toThrow('unknown session');
    expect(() => manager.resize(2, 'one', 80, 24)).toThrow('unknown session');
    expect(() => manager.stop(2, 'one')).toThrow('unknown session');
    expect(() => manager.status(1, 'missing')).toThrow('unknown session');
    expect(() => manager.subscribe(2, subscription('one', 'output', 'x'))).toThrow('unknown session');
  });
  it('rejects duplicate and stale subscriptions', () => {
    const manager = new SessionManager(() => new FakeProcess(), () => undefined);
    manager.start(1, start('one')); const sub = subscription('one', 'output', 'same');
    manager.subscribe(1, sub);
    expect(() => manager.subscribe(1, sub)).toThrow('already exists');
    expect(() => manager.unsubscribe(2, sub)).toThrow('unknown subscription');
    manager.unsubscribe(1, sub);
    expect(() => manager.unsubscribe(1, sub)).toThrow('unknown subscription');
  });
  it('kills owned processes and prevents delivery after teardown', () => {
    const process = new FakeProcess(); const delivered: unknown[] = [];
    const manager = new SessionManager(() => process, (...args) => delivered.push(args));
    manager.start(1, start('one')); manager.subscribe(1, subscription('one', 'output', 'x'));
    manager.teardownOwner(1); process.emitData('stale'); process.emitExit(9);
    expect(process.killed).toBe(true); expect(delivered).toEqual([]);
    expect(() => manager.input(1, 'one', 'stale')).toThrow('unknown session');
  });
  it('stop tears down subscriptions and makes later messages stale', () => {
    const process = new FakeProcess(); const delivered: unknown[] = [];
    const manager = new SessionManager(() => process, (...args) => delivered.push(args));
    manager.start(1, start('one')); manager.subscribe(1, subscription('one', 'exit', 'x'));
    manager.stop(1, 'one'); process.emitExit(0);
    expect(process.killed).toBe(true); expect(delivered).toEqual([]);
    expect(() => manager.status(1, 'one')).toThrow('unknown session');
  });
  it('buffers startup output and exit until renderer subscriptions are attached', () => {
    const process = new FakeProcess(); const delivered: unknown[] = [];
    const manager = new SessionManager(() => process, (...args) => delivered.push(args));
    manager.start(1, start('one')); process.emitData('early'); process.emitExit(7);
    manager.subscribe(1, subscription('one', 'output', 'output'));
    manager.subscribe(1, subscription('one', 'exit', 'exit'));
    expect(delivered).toEqual([
      [1, 'terminal:output', { sessionId: 'one', data: 'early' }],
      [1, 'terminal:exit', { sessionId: 'one', exitCode: 7 }],
    ]);
    expect(() => manager.unsubscribe(1, subscription('one', 'output', 'output'))).toThrow('unknown subscription');
  });
  it('routes read-only lifecycle status without consulting adversarial terminal output', () => {
    const chat = '123e4567-e89b-42d3-a456-426614174000';
    const delivered: unknown[] = []; const holder: { manager?: SessionManager } = {};
    const store = new StatusChannelStore(path.join(os.tmpdir(), `void-status-${crypto.randomUUID()}`), (owner, event) => holder.manager!.lifecycleChanged(owner, event), () => false);
    let generation = 0; const process = new FakeProcess();
    const manager = holder.manager = new SessionManager((_request, authority) => { generation = authority!.generation; return process; }, (...args) => delivered.push(args), store);
    manager.start(1, { sessionId: chat, cwd: '/tmp', mode: 'create' });
    manager.subscribe(1, subscription(chat, 'status', 'status-sub'));
    expect(manager.lifecycleStatus(1, chat)).toMatchObject({ state: 'running', diagnostic: expect.any(String) });
    expect(store.ingest(chat, { version: 1, chatId: chat, generation, sequence: 1, state: 'Working', timestamp: '2026-07-25T00:00:00Z' })).toBe(true);
    expect(delivered.at(-1)).toEqual([1, 'chat:lifecycle', { sessionId: chat, state: 'working', unread: false }]);
    process.emitData('{"state":"Ready","sequence":999} prompt Working Ready');
    expect(manager.lifecycleStatus(1, chat).state).toBe('working');
    manager.stop(1, chat);
    expect(store.ingest(chat, { version: 1, chatId: chat, generation, sequence: 2, state: 'Ready', timestamp: '2026-07-25T00:00:01Z' })).toBe(false);
  });
  it('tears down every owner on app quit without touching unrelated managers', () => {
    const ownedOne = new FakeProcess(); const ownedTwo = new FakeProcess(); const unrelated = new FakeProcess();
    const processes = [ownedOne, ownedTwo];
    const manager = new SessionManager(() => processes.shift()!, () => undefined);
    const baseline = new SessionManager(() => unrelated, () => undefined);
    manager.start(1, start('one')); manager.start(2, start('two')); baseline.start(3, start('baseline'));
    manager.teardownAll();
    expect([ownedOne.killed, ownedTwo.killed, unrelated.killed]).toEqual([true, true, false]);
  });
});

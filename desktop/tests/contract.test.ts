import { describe, expect, it } from 'vitest';
import { inputRequest, resizeRequest, sessionRequest, startRequest, subscribeRequest } from '../src/shared/contract';

describe('allowlisted renderer contract validation', () => {
  it('accepts only the owned harmless fixture', () => {
    expect(startRequest({ sessionId: 'owned-1', fixture: 'roundTrip' })).toEqual({ sessionId: 'owned-1', fixture: 'roundTrip' });
    for (const request of [
      { sessionId: 'x', fixture: '/bin/sh' },
      { sessionId: 'x', fixture: 'roundTrip', command: '/bin/sh' },
      { sessionId: 'x', fixture: 'roundTrip', path: '/tmp' },
      { sessionId: '../escape', fixture: 'roundTrip' },
    ]) expect(() => startRequest(request)).toThrow();
  });
  it('rejects malformed, oversized, and unknown input fields', () => {
    expect(() => inputRequest({ sessionId: 'x', data: 'a'.repeat(65_537) })).toThrow('invalid input');
    expect(() => inputRequest({ sessionId: 'x', data: 'ok', shell: true })).toThrow('unknown');
    expect(() => sessionRequest({ sessionId: '' })).toThrow('invalid sessionId');
    expect(() => sessionRequest({ sessionId: 'x', command: 'whoami' })).toThrow('unknown');
  });
  it('bounds every resize argument', () => {
    expect(resizeRequest({ sessionId: 'x', cols: 80, rows: 24 })).toMatchObject({ cols: 80, rows: 24 });
    for (const size of [[1, 24], [80, 0], [1001, 24], [80.5, 24], ['80', 24]]) {
      expect(() => resizeRequest({ sessionId: 'x', cols: size[0], rows: size[1] })).toThrow('invalid terminal size');
    }
  });
  it('requires exact subscription fields and unguessable IDs', () => {
    const valid = { sessionId: 'x', kind: 'output', subscriptionId: '123e4567-e89b-12d3-a456-426614174000' };
    expect(subscribeRequest(valid)).toEqual(valid);
    expect(() => subscribeRequest({ ...valid, kind: 'raw-process' })).toThrow();
    expect(() => subscribeRequest({ ...valid, subscriptionId: '1' })).toThrow();
    expect(() => subscribeRequest({ ...valid, callback: 'eval()' })).toThrow();
  });
});

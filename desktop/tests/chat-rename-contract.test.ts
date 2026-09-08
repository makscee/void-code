import { describe, expect, it } from 'vitest';
import * as contract from '../src/shared/contract';

const CHAT = '123e4567-e89b-42d3-a456-426614174000';
type RenameRequest = (value: unknown) => { sessionId: string; title: string };

function renameRequest(value: unknown) {
  const validator = (contract as unknown as { renameRequest?: RenameRequest }).renameRequest;
  if (typeof validator !== 'function') throw new Error('shared contract exports no renameRequest(value) validator');
  return validator(value);
}

describe('chat rename IPC trust boundary', () => {
  it('returns only the strict UUID and a trimmed title', () => {
    expect(renameRequest({ sessionId: CHAT, title: '  Quarterly close  ' })).toEqual({ sessionId: CHAT, title: 'Quarterly close' });
  });

  it('accepts the 80-character boundary after trimming', () => {
    const title = 'x'.repeat(80);
    expect(renameRequest({ sessionId: CHAT, title: ` ${title} ` })).toEqual({ sessionId: CHAT, title });
  });

  it.each([
    ['empty', ''],
    ['whitespace', ' \t\n '],
    ['81 characters', 'x'.repeat(81)],
    ['non-string', 42],
  ])('rejects an %s title', (_label, title) => {
    expect(() => renameRequest({ sessionId: CHAT, title })).toThrow('invalid title');
  });

  it('rejects malformed chat IDs', () => {
    expect(() => renameRequest({ sessionId: '../chat', title: 'Safe' })).toThrow('invalid sessionId');
  });

  it('rejects unknown or missing fields rather than forwarding them', () => {
    expect(() => renameRequest({ sessionId: CHAT, title: 'Safe', path: '/tmp/session.jsonl' })).toThrow('unknown');
    expect(() => renameRequest({ sessionId: CHAT })).toThrow('missing');
  });
});

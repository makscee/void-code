import { describe, expect, it } from 'vitest';
import { createLoginDiagnosticsStore } from '../src/main/auth-diagnostics';

// The real failure this pins: a login's stderr text ("device authorization rate limited")
// and malformed-output lines are the only trace of why a login silently did nothing. They
// must survive past the moment the login attempt ends, so a person can hand them over —
// but the source is a long-running child process's stderr, which is exactly the shape of
// thing that leaks memory if nothing bounds it. Every test here pins a bound, not just retention.

const LOGIN_A = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa';
const LOGIN_B = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb';

describe('createLoginDiagnosticsStore', () => {
  it('retains recorded lines for a login after the attempt ends, in order', () => {
    const store = createLoginDiagnosticsStore();
    store.record(LOGIN_A, 'malformed login output: not json at all');
    store.record(LOGIN_A, 'device authorization rate limited');
    // Nothing about "the attempt ended" clears the store — get() is a later, separate read.
    expect(store.get(LOGIN_A)).toEqual(['malformed login output: not json at all', 'device authorization rate limited']);
  });

  it('returns an empty list for a loginId nothing was ever recorded against', () => {
    const store = createLoginDiagnosticsStore();
    expect(store.get('never-seen')).toEqual([]);
  });

  it('keeps only the most recent lines once a single login exceeds the per-login bound', () => {
    // Default bound is 20 lines per login — a runaway or chatty child process must not grow
    // this without limit while a login is in progress.
    const store = createLoginDiagnosticsStore();
    for (let index = 0; index < 25; index++) store.record(LOGIN_A, `line-${index}`);
    const kept = store.get(LOGIN_A);
    expect(kept).toHaveLength(20);
    expect(kept[0]).toBe('line-5');
    expect(kept[19]).toBe('line-24');
  });

  it('truncates a single overlong line instead of retaining it whole or dropping it', () => {
    // A stack dump or binary garbage arriving as one 'data' chunk must not itself be the leak.
    const store = createLoginDiagnosticsStore();
    const huge = 'x'.repeat(2000);
    store.record(LOGIN_A, huge);
    const [kept] = store.get(LOGIN_A);
    expect(kept).toHaveLength(500);
    expect(kept).toBe(huge.slice(0, 500));
  });

  it('evicts the oldest login once more than the login-count bound have been recorded against', () => {
    // Default bound is 5 distinct logins retained — a person retrying login repeatedly must not
    // make this store grow forever across attempts either.
    const store = createLoginDiagnosticsStore();
    const ids = ['id-0', 'id-1', 'id-2', 'id-3', 'id-4', 'id-5'];
    for (const id of ids) store.record(id, `diagnostic for ${id}`);
    expect(store.get('id-0')).toEqual([]); // evicted: oldest login, over the bound
    expect(store.get('id-5')).toEqual([`diagnostic for id-5`]);
    expect(store.get('id-1')).toEqual([`diagnostic for id-1`]); // still within the 5 most recent
  });

  it('does not evict a login just because it keeps receiving new lines', () => {
    // Recording again against an already-tracked login is not the same as starting a new one —
    // it must not push other logins toward eviction on every stderr chunk.
    const store = createLoginDiagnosticsStore();
    for (const id of ['id-0', 'id-1', 'id-2', 'id-3', 'id-4']) store.record(id, `first for ${id}`);
    store.record('id-0', 'second for id-0');
    store.record('id-0', 'third for id-0');
    expect(store.get('id-1')).toEqual(['first for id-1']);
    expect(store.get('id-0')).toEqual(['first for id-0', 'second for id-0', 'third for id-0']);
  });

  it('accepts explicit bounds instead of the defaults', () => {
    const store = createLoginDiagnosticsStore(2, 1, 5);
    store.record(LOGIN_A, 'one');
    store.record(LOGIN_A, 'two');
    store.record(LOGIN_A, 'three-longer');
    expect(store.get(LOGIN_A)).toEqual(['two', 'three']); // per-login bound 2, line length bound 5

    store.record(LOGIN_B, 'anything');
    expect(store.get(LOGIN_A)).toEqual([]); // login-count bound 1: LOGIN_A evicted for LOGIN_B
    expect(store.get(LOGIN_B)).toEqual(['anyth']);
  });

  it('keeps two logins independent of each other', () => {
    const store = createLoginDiagnosticsStore();
    store.record(LOGIN_A, 'a-only');
    store.record(LOGIN_B, 'b-only');
    expect(store.get(LOGIN_A)).toEqual(['a-only']);
    expect(store.get(LOGIN_B)).toEqual(['b-only']);
  });
});

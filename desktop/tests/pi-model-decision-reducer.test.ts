import { describe, expect, it } from 'vitest';
import { A, B, F, I64, NS, acknowledge, assertClosed, bootstrap, canonical, commit, config, decision, digest, encoded, envelope, headerNames, initial as rawInitial, legacy, models, observation, observe, reducer, pinned, checkpoint, customType, repo } from './fixtures/pi-model-decision';
import type { Authority, Decision, Envelope, Reducer, State, Token } from './fixtures/pi-model-decision';

// Pure protocol sequence only; production witnesses restore via real Pi events.
const initial = (r: Reducer, id = 'fixture-controller-A'): State => {
  const s = rawInitial(r, id);
  return commit(r, s, { type: 'activeBranchRestored', reason: 'startup', branchSnapshot: { sessionId: `${id}-session`, leafId: null, entries: [] } }, 0n);
};
const active = (r: Reducer, d = decision()): State => acknowledge(r, observe(r, initial(r), d));
const select = (r: Reducer, s: State, id: string): State => commit(r, s, { type: 'localModelSelected', providerId: 'void-codex', modelId: id, source: 'set', ownEffectToken: null });
const fresh = (d: Decision, evaluatedAt = '2026-09-16T12:01:00.000000000Z', validUntil = '2026-09-16T12:03:00.000000000Z'): Decision => ({ ...structuredClone(d), evaluatedAt, validUntil });
const empty = (generation: string): Decision => { const d = decision(generation); d.outcome = 'empty_compatible_catalog'; d.authority.allowedCodexModelIds = []; d.authority.defaultCodexModelId = d.authority.effectiveCodexModelId = null; return d; };

// Generation spelling is authority, not a floating point number or permissive BigInt parse.
describe('R1/R5 canonical generation and closed codec', () => {
  it.each([12, ['12'], null].map(value => ({ value })))('generation JSON type $value must not be coerced into a canonical string', async ({ value }) => {
    const r = await reducer(); const s = active(r); const d = decision('12'); Object.assign(d, { generation: value });
    expect(r.parseDecision(d, models, config).ok).toBe(false); const got = observe(r, s, d);
    expect(got.highestSeenGeneration).toBe('11'); expect(got.leaseDeadline).toBe(s.leaseDeadline); expect(got.acceptedAuthority).toEqual(s.acceptedAuthority);
  });
  it('raw initialization has zero revisions/ordinals, no pending token and no authority before restoration', async () => {
    const r = await reducer(); const s = rawInitial(r);
    expect(s.stateRevision).toBe(0n); expect(s.branchContextEpoch).toBe(0n); expect(s.lastAllocatedOrdinal).toBe(0n); expect(s.pendingEffectPlan).toBeNull(); expect(s.leaseDeadline).toBeNull(); assertClosed(s);
  });
  it.each(['effectiveAssignmentRevision', 'assignmentHeadRevision', 'policyRevision', 'calibrationRevision', 'providerGrantSetRevision', 'poolRevision', 'poolCollectionRevision', 'controlRevision', 'controlEpoch', 'quotaLatchRevision', 'quotaEpisode'].flatMap(field => ['0', '01', '9223372036854775808'].map(value => ({ field, value }))))('revision grammar $field=$value quarantines canonical newer decision', async ({ field, value }) => {
    const r = await reducer(); const d = decision('12'); Object.assign(d.authority, { [field]: value });
    const s = observe(r, active(r), d); expect(s.highestSeenGeneration).toBe('12'); expect(s.authorityStatus).toBe('quarantined'); assertClosed(s);
  });
  it.each([null, [], 7, false, '{not JSON'])('unidentifiable readback %s cannot reset authority or renew lease', async input => {
    const r = await reducer(); const s = active(r); const got = observe(r, s, input, 30n * NS);
    expect(got.highestSeenGeneration).toBe(s.highestSeenGeneration); expect(got.acceptedAuthority).toEqual(s.acceptedAuthority); expect(got.leaseDeadline).toBe(s.leaseDeadline); expect(got.lastAllocatedOrdinal).toBe(s.lastAllocatedOrdinal);
  });
  it.each(Object.keys(decision().authority))('missing authority member %s cannot be reconstructed from previous state', async key => {
    const r = await reducer(); const d = decision('12'); Reflect.deleteProperty(d.authority, key);
    const s = observe(r, active(r), d); expect(s.highestSeenGeneration).toBe('12'); expect(s.authorityStatus).toBe('quarantined'); assertClosed(s);
  });
  it('all delivery permutations converge on maximum generation without leaking stale catalog fields', async () => {
    const r = await reducer(); const bundles = [decision('2'), decision('10', true), decision('3')];
    for (const order of [[0, 1, 2], [0, 2, 1], [1, 0, 2], [1, 2, 0], [2, 0, 1], [2, 1, 0]]) {
      let s = initial(r); for (const index of order) s = observe(r, s, bundles[index]);
      expect(s.highestSeenGeneration).toBe('10'); expect(s.acceptedAuthority).toEqual(bundles[1].authority); expect(s.desiredTarget.selectionModelId).toBe(F); assertClosed(s);
    }
  });
  it.each(['1', '9', '10', '9007199254740993', '9223372036854775807'])('accepts exact generation %s', async generation => {
    const r = await reducer();
    expect(r.parseDecision(decision(generation), models, config)).toMatchObject({ ok: true, decision: { generation } });
  });
  it.each(['', '0', '-1', '+1', '01', ' 1', '1 ', '1.0', '1e1', 'x', '9223372036854775808', '99999999999999999999', '1'.repeat(100_000)])('rejects noncanonical generation %.30s without changing floor', async generation => {
    const r = await reducer(); const s = active(r);
    const got = observe(r, s, decision(generation));
    expect(got.highestSeenGeneration).toBe('11');
    expect(got.leaseDeadline).toBe(s.leaseDeadline);
    expect(r.parseDecision(decision(generation), models, config).ok).toBe(false);
  });
  it.each(['generation', 'authority', 'evaluatedAt', 'validUntil', 'outcome', 'schemaVersion'])('missing %s is never spliced from prior authority', async key => {
    const r = await reducer(); const d: Record<string, unknown> = { ...decision('12') }; delete d[key];
    expect(r.parseDecision(d, models, config).ok).toBe(false);
    const s = observe(r, active(r), d);
    expect(s.highestSeenGeneration).toBe(key === 'generation' ? '11' : '12');
    if (key !== 'generation') { expect(s.authorityStatus).toBe('quarantined'); assertClosed(s); }
  });
  it.each(['outer', 'authority', 'successor'])('unknown %s keys fail closed at canonical generation', async location => {
    const r = await reducer(); const d = decision('12');
    if (location === 'outer') Object.assign(d, { unknown: true });
    if (location === 'authority') Object.assign(d.authority, { unknown: true });
    if (location === 'successor') d.authority.scheduledSuccessor = Object.assign({ assignmentRevision: '2', effectiveAt: d.validUntil, tierModelSetDigest: 'next' }, { unknown: true });
    const s = observe(r, active(r), d); expect(s.highestSeenGeneration).toBe('12'); expect(s.authorityStatus).toBe('quarantined'); assertClosed(s);
  });
  it.each([
    ['version', 1], ['version', 3], ['modelDecision', undefined],
    ['pollIntervalSeconds', '0'], ['pollIntervalSeconds', '301'], ['pollIntervalSeconds', '01'],
    ['catalogDecisionTtlSeconds', '2147483648'], ['catalogDecisionTtlSeconds', '-1'],
    ['catalogExpirySkewSeconds', '120'], ['catalogExpirySkewSeconds', '-1'], ['readbackUrl', '/relative'],
  ])('bootstrap rejects %s=%s rather than guessing product configuration', async (field, value) => {
    const r = await reducer(); const b = structuredClone(bootstrap);
    Object.assign(field === 'version' || field === 'modelDecision' ? b : b.modelDecision, { [field]: value });
    expect(r.parseBootstrap(b).ok).toBe(false);
  });
  it('bootstrap ceiling does not grant initial permission; canonical bytes ignore object insertion order', async () => {
    const r = await reducer(); expect(r.parseBootstrap(bootstrap).ok).toBe(true);
    const s = initial(r); assertClosed(s); expect(s.highestSeenGeneration).toBeNull(); expect(s.leaseDeadline).toBeNull();
    const d = decision(); const reversed = Object.fromEntries(Object.entries(d.authority).reverse());
    expect(r.canonicalSerialize(d.authority)).toBe(canonical(reversed));
    expect(r.canonicalSerialize([...models].reverse())).not.toBe(r.canonicalSerialize(models));
  });
});

const authorityChanges: [string, (a: Authority) => void][] = [
  ...(['effectiveAssignmentRevision', 'assignmentHeadRevision', 'policyRevision', 'calibrationRevision', 'providerGrantSetRevision', 'poolRevision', 'poolCollectionRevision', 'controlRevision', 'controlEpoch', 'quotaLatchRevision'] as const).map(key => [key, (a: Authority): void => { a[key] = '99'; }] as [string, (a: Authority) => void]),
  ['tierId', a => { a.tierId = 'other'; }], ['tierModelSetDigest', a => { a.tierModelSetDigest = 'other'; }], ['inputFingerprint', a => { a.inputFingerprint = 'other'; }], ['quotaEpisode', a => { a.quotaEpisode = '12'; }],
  ['controlMode', a => { a.controlMode = 'warn'; }], ['quotaState', a => { a.quotaState = 'warning'; }], ['restrictionActive', a => { a.restrictionActive = true; }],
  ['allowed order', a => { a.allowedCodexModelIds.reverse(); }], ['default', a => { a.defaultCodexModelId = A; }], ['effective', a => { a.effectiveCodexModelId = A; }], ['fallback', a => { a.fallbackCodexModelId = B; }],
  ['successor null→value', a => { a.scheduledSuccessor = { assignmentRevision: '99', effectiveAt: '2026-09-17T00:00:00.000000000Z', tierModelSetDigest: 'next' }; }],
];
describe('R1 evidence and complete atomic authority', () => {
  it.each(authorityChanges)('equal-generation one-field conflict: %s quarantines', async (_name, change) => {
    const r = await reducer(); const d = decision(); const s = active(r, d); const changed = structuredClone(d); change(changed.authority);
    const got = observe(r, s, changed); expect(got.authorityStatus).toBe('quarantined'); expect(got.highestSeenGeneration).toBe('11'); assertClosed(got);
    expect(observe(r, got, d).authorityStatus).toBe('quarantined');
  });
  it.each(['assignmentRevision', 'effectiveAt', 'tierModelSetDigest', 'null'])('successor %s participates in atomic equality', async key => {
    const r = await reducer(); const d = decision(); d.authority.scheduledSuccessor = { assignmentRevision: '12', effectiveAt: '2026-09-17T00:00:00.000000000Z', tierModelSetDigest: 'next' };
    const s = active(r, d); const changed = structuredClone(d);
    if (key === 'null') changed.authority.scheduledSuccessor = null;
    else Object.assign(changed.authority.scheduledSuccessor!, { [key]: key === 'effectiveAt' ? '2026-09-18T00:00:00.000000000Z' : key === 'assignmentRevision' ? '13' : 'different' });
    expect(observe(r, s, changed).authorityStatus).toBe('quarantined');
  });
  it('outcome difference quarantines, not an empty renewal', async () => {
    const r = await reducer(); expect(observe(r, active(r), empty('11')).authorityStatus).toBe('quarantined');
  });
  it('exact cached evidence cannot move deadline when replayed later', async () => {
    const r = await reducer(); const s = active(r); const got = commit(r, s, observation(s, decision(), 'readback', 30n * NS, 32n * NS), 32n * NS);
    expect(got.leaseDeadline).toBe(s.leaseDeadline); expect(got.lastAllocatedOrdinal).toBe(s.lastAllocatedOrdinal); expect(got.appliedEffects).toEqual(s.appliedEffects);
  });
  it('newer evaluation renews measured evidence but shorter renewal REPLACES deadline', async () => {
    const r = await reducer(); const s = active(r); const d = fresh(decision(), '2026-09-16T12:01:00.000000000Z', '2026-09-16T12:01:10.000000000Z');
    const got = commit(r, s, observation(s, d, 'readback', 30n * NS, 32n * NS), 32n * NS);
    expect(got.leaseDeadline).toBe(38n * NS); expect(got.leaseDeadline! < s.leaseDeadline!).toBe(true); expect(got.lastAllocatedOrdinal).toBe(s.lastAllocatedOrdinal);
  });
  it('strictly newer valid evidence can extend, stale evaluation cannot refresh display or authority', async () => {
    const r = await reducer(); const s = active(r); const d = fresh(decision());
    const got = commit(r, s, observation(s, d, 'readback', 30n * NS, 32n * NS), 32n * NS);
    expect(got.leaseDeadline).toBe(148n * NS);
    const stale = observe(r, got, { ...decision(), display: { pct: 999 } }, 33n * NS);
    expect(stale.lastLeaseEvidence).toEqual(got.lastLeaseEvidence); expect(stale.leaseDeadline).toBe(got.leaseDeadline); expect(stale.desiredTarget).toEqual(got.desiredTarget);
  });
  it('same evaluatedAt plus different expiry quarantines', async () => {
    const r = await reducer(); const d = decision(); d.validUntil = '2026-09-16T12:01:59.000000000Z';
    const got = observe(r, active(r), d); expect(got.authorityStatus).toBe('quarantined'); assertClosed(got);
  });
  it.each(['invalid', 'conflict', 'empty'])('%s latches 12; delayed 11/equal 12 cannot recover; 13 can only apply in phase B', async kind => {
    const r = await reducer(); let s = active(r);
    if (kind === 'empty') s = observe(r, s, empty('12'));
    else { const d = decision('12'); if (kind === 'conflict') s = observe(r, s, d); d.schemaVersion = 2; s = observe(r, s, d); }
    expect(s.highestSeenGeneration).toBe('12'); assertClosed(s);
    for (const generation of ['11', '12']) { s = observe(r, s, decision(generation)); expect(s.highestSeenGeneration).toBe('12'); assertClosed(s); }
    s = observe(r, s, decision('13')); assertClosed(s); s = acknowledge(r, s); expect(s.selectableModelIds).toEqual([A, B, F]);
  });
  it('ordering above 2^53 and different string lengths is exact', async () => {
    const r = await reducer(); let s = observe(r, initial(r), decision('9')); s = observe(r, s, decision('10')); expect(s.highestSeenGeneration).toBe('10');
    s = observe(r, s, decision('9007199254740993', true)); const target = s.desiredTarget;
    s = observe(r, s, decision('9007199254740992')); expect(s.highestSeenGeneration).toBe('9007199254740993'); expect(s.desiredTarget).toEqual(target);
  });
});

describe('R1 complete tuple and monotonic lease boundaries', () => {
  it.each([
    ['future epoch with short TTL', '2399-01-01T00:00:00.000000000Z', '2399-01-01T00:01:00.000000000Z', 10n * NS, 10n * NS, '0'],
    ['past epoch with short TTL', '1600-01-01T00:00:00.000000000Z', '1600-01-01T00:01:00.000000000Z', 10n * NS, 10n * NS, '0'],
    ['signed subtraction wrap/abs', '1677-09-21T00:12:43.145224192Z', '2262-04-11T23:47:16.854775807Z', 10n * NS, 10n * NS, '0'],
    ['evaluatedAt fractional precision', '2026-09-16T12:00:00.000000001Z', '2026-09-16T12:00:25.000000001Z', 100n * NS, 120n * NS, '5'],
  ] as const)('%s cannot hide behind a second invalid field or rounded duration', async (_name, evaluatedAt, validUntil, start, receipt, skew) => {
    const r = await reducer(); const s = initial(r); const d = { ...decision('12'), evaluatedAt, validUntil };
    const e = observation(s, d, 'readback', start, receipt); e.config = { ...config, catalogExpirySkewSeconds: skew };
    const got = commit(r, s, e, receipt); expect(got.highestSeenGeneration).toBe('12'); expect(got.authorityStatus).toBe('quarantined'); assertClosed(got);
  });
  it.each(['shadow', 'warn', 'active'].flatMap(mode => ['normal', 'warning', 'fallback'].map(quota => [mode, quota])))('%s/%s exact restriction formula; inverted boolean rejects', async (mode, quota) => {
    const r = await reducer(); const restricted = mode === 'active' && quota === 'fallback'; const d = decision('12', restricted);
    d.authority.controlMode = mode; d.authority.quotaState = quota;
    if (quota !== 'normal') d.authority.quotaEpisode = '1';
    expect(r.parseDecision(d, models, config).ok).toBe(true);
    d.authority.restrictionActive = !restricted; expect(r.parseDecision(d, models, config).ok).toBe(false);
  });
  it.each([
    ['restricted multimodel', (d: Decision) => { d.authority.allowedCodexModelIds = [F, A]; }],
    ['restricted nonfallback default', (d: Decision) => { d.authority.defaultCodexModelId = A; }],
    ['missing fallback', (d: Decision) => { Object.assign(d.authority, { fallbackCodexModelId: undefined }); }],
    ['duplicate', (d: Decision) => { d.authority.allowedCodexModelIds = [F, F]; }],
    ['empty ID', (d: Decision) => { d.authority.allowedCodexModelIds = ['']; }],
    ['unknown mode', (d: Decision) => { d.authority.controlMode = 'ACTIVE'; }],
    ['unknown latch', (d: Decision) => { d.authority.quotaState = 'FALLBACK'; }],
    ['oversized model', (d: Decision) => { d.authority.allowedCodexModelIds = ['x'.repeat(129)]; }],
  ] as const)('invalid tuple: %s quarantines greater generation', async (_name, mutate) => {
    const r = await reducer(); const d = decision('12', true); mutate(d); const s = observe(r, active(r), d); expect(s.highestSeenGeneration).toBe('12'); expect(s.authorityStatus).toBe('quarantined'); assertClosed(s);
  });
  it.each([
    ['negative start', -1n, 0n, '2', '2026-09-16T12:02:00.000000000Z'],
    ['backward RTT', 13n * NS, 12n * NS, '2', '2026-09-16T12:02:00.000000000Z'],
    ['negative skew', 0n, 1n, '-1', '2026-09-16T12:02:00.000000000Z'],
    ['seconds conversion overflow', 0n, 1n, '9223372037', '2026-09-16T12:02:00.000000000Z'],
    ['deadline overflow', I64 - NS, I64, '0', '2026-09-16T12:02:00.000000000Z'],
    ['mono overflow', I64 + 1n, I64 + 1n, '0', '2026-09-16T12:02:00.000000000Z'],
    ['zero ttl', 0n, 0n, '0', '2026-09-16T12:00:00.000000000Z'],
    ['negative ttl', 0n, 0n, '0', '2026-09-16T11:59:59.999999999Z'],
    ['RTT plus skew consumes all', 0n, 118n * NS, '2', '2026-09-16T12:02:00.000000000Z'],
    ['ttl over configured maximum', 0n, 0n, '0', '2026-09-16T12:02:00.000000001Z'],
    ['timestamp overflow', 0n, 0n, '0', '9999-09-16T12:02:00.000000000Z'],
    ['timestamp precision lost', 0n, 0n, '0', '2026-09-16T12:02:00.0000000001Z'],
    ['non UTC', 0n, 0n, '0', '2026-09-16T12:02:00+00:00'],
  ] as const)('%s cannot widen lease', async (_name, start, receipt, skew, expiry) => {
    const r = await reducer(); const s = initial(r); const d = decision('12'); d.validUntil = expiry;
    const event = observation(s, d, 'readback', start, receipt); event.config = { ...config, catalogExpirySkewSeconds: skew };
    const got = commit(r, s, event, receipt); assertClosed(got); expect(got.authorityStatus).toBe('quarantined');
  });
  it('one nanosecond survives exact arithmetic and expires at deadline even if timer arrives late', async () => {
    const r = await reducer(); const s = initial(r); const d = decision(); d.validUntil = '2026-09-16T12:00:25.000000001Z';
    const e = observation(s, d, 'readback', 100n * NS, 120n * NS); e.config = { ...config, catalogExpirySkewSeconds: '5' };
    const pending = commit(r, s, e, 120n * NS); expect(pending.leaseDeadline).toBe(120n * NS + 1n);
    const applied = acknowledge(r, pending, 120n * NS); expect(applied.selectableModelIds).toEqual([A, B, F]);
    const exact = commit(r, applied, { type: 'leaseTick' }, 120n * NS + 1n); expect(exact.authorityStatus).toBe('expired'); assertClosed(exact);
    assertClosed(commit(r, applied, { type: 'leaseTick' }, 999n * NS));
    assertClosed(acknowledge(r, pending, 120n * NS + 1n));
  });
});

describe('R1 local restoration memory across real authority edges', () => {
  it.each([true, false])('fallback→forbidden attempt→warn/fallback restores only newly entitled memory (retained=%s)', async retained => {
    const r = await reducer(); let s = select(r, active(r), A);
    s = acknowledge(r, observe(r, s, decision('12', true))); expect(s.preRestrictionModelId).toBe(A);
    s = select(r, s, B); expect(s.preRestrictionModelId).toBe(A);
    s = observe(r, s, decision('13', true)); expect(s.preRestrictionModelId).toBe(A); s = acknowledge(r, s);
    const exit = decision('14'); Object.assign(exit.authority, { controlMode: 'warn', controlRevision: '9', controlEpoch: '10', quotaState: 'fallback', quotaEpisode: '1', allowedCodexModelIds: retained ? [A, B, F] : [B, F] });
    s = observe(r, s, exit); expect(s.desiredTarget.selectionModelId).toBe(retained ? A : B); expect(s.preRestrictionModelId).toBe(A); assertClosed(s);
    const failed = commit(r, s, { type: 'piEffectsFailed', effectPlanToken: s.pendingEffectPlan, stage: 'set_model', boundedErrorClass: 'fixture' });
    expect(failed.preRestrictionModelId).toBe(A);
    s = commit(r, failed, { type: 'retryPiEffects', expectedTargetKey: failed.desiredTarget }); s = acknowledge(r, s); expect(s.preRestrictionModelId).toBeNull();
  });
  it('unrestricted entitlement narrowing neither remembers removed choice nor hardcodes fallback', async () => {
    const r = await reducer(); let s = select(r, active(r), A); const d = decision('12'); d.authority.allowedCodexModelIds = [B, F];
    s = observe(r, s, d); expect(s.desiredTarget.selectionModelId).toBe(B); expect(s.preRestrictionModelId).toBeNull();
  });
  it('independent session memories do not cross; un-applied initial model is never captured', async () => {
    const r = await reducer(); const left = select(r, active(r), A); const right = select(r, acknowledge(r, observe(r, initial(r, 'other-controller'), decision())), B);
    const a = observe(r, left, decision('12', true)); const b = observe(r, right, decision('12', true));
    expect([a.preRestrictionModelId, b.preRestrictionModelId]).toEqual([A, B]);
    const exitA = observe(r, acknowledge(r, a), decision('13')); const exitB = observe(r, acknowledge(r, b), decision('13'));
    expect([exitA.desiredTarget.selectionModelId, exitB.desiredTarget.selectionModelId]).toEqual([A, B]);
    const restoredA = acknowledge(r, exitA); const restoredB = acknowledge(r, exitB);
    expect([restoredA.appliedEffects.target?.selectionModelId, restoredB.appliedEffects.target?.selectionModelId]).toEqual([A, B]);
    expect(observe(r, initial(r), decision('12', true)).preRestrictionModelId).toBeNull();
  });
});

const allocationFaults: [string, (e: Envelope) => void][] = [
  ['missing', e => { Reflect.deleteProperty(e, 'allocation'); }], ['none', e => { e.allocation = { kind: 'none' }; }],
  ['duplicate ordinal', e => { if (e.allocation.kind === 'apply_pi_catalog') e.allocation.token.planOrdinal = 0n; }],
  ['skipped ordinal', e => { if (e.allocation.kind === 'apply_pi_catalog') e.allocation.token.attemptOrdinal += 2n; }],
  ['wrong target', e => { if (e.allocation.kind === 'apply_pi_catalog') e.allocation.token.target.selectionModelId = A; }],
  ['wrong catalog digest', e => { if (e.allocation.kind === 'apply_pi_catalog') e.allocation.token.target.catalogDigest += 'x'; }],
];
describe('R2 token envelope and two-phase protocol', () => {
  it.each(['protocolVersion', 'event', 'commitMonoNs'])('missing current envelope %s is bounded failed-closed, not a thrown decoder exception', async field => {
    const r = await reducer(); const s = active(r); const e = envelope(r, s, observation(s, decision('12', true))); Reflect.deleteProperty(e, field);
    const got = r.reduce(s, e); assertClosed(got.state); expect(got.state.appliedEffects.status).toBe('effect_failed_closed'); expect(got.effects.filter(effect => effect.type === 'applyPiCatalog')).toEqual([]);
    expect(BigInt(got.state.highestSeenGeneration!)).toBeGreaterThanOrEqual(11n); expect(got.state.preRestrictionModelId).toBe(s.preRestrictionModelId);
  });
  it('process-local ordinal above 2^53 is neither converted nor reset on a new target', async () => {
    const r = await reducer(); const s = { ...active(r), stateRevision: 9007199254741013n, lastAllocatedOrdinal: 9007199254740993n };
    const e = envelope(r, s, observation(s, decision('12', true))); const got = r.reduce(s, e).state;
    expect(got.lastAllocatedOrdinal).toBe(9007199254740994n); expect(got.pendingEffectPlan?.planOrdinal).toBe(9007199254740994n); expect(got.pendingEffectPlan?.attemptOrdinal).toBe(9007199254740994n);
  });
  it('branch transition uses PRE epoch in envelope and POST epoch in token; ordinal cannot reset', async () => {
    const r = await reducer(); const p = await pinned(); const sm = p.SessionManager.inMemory(repo); sm.appendCustomEntry(customType, checkpoint(sm));
    const s = active(r); const e = envelope(r, s, { type: 'activeBranchRestored', reason: 'tree', branchSnapshot: { sessionId: sm.getSessionId(), leafId: sm.getLeafId(), entries: structuredClone(sm.getBranch()) } });
    expect(e.branchContextEpoch).toBe(s.branchContextEpoch); expect(e.allocation.kind).toBe('apply_pi_catalog');
    if (e.allocation.kind !== 'apply_pi_catalog') throw new Error('required branch intent');
    expect(e.allocation.token.target.branchContextEpoch).toBe(s.branchContextEpoch + 1n); expect(e.allocation.token.planOrdinal).toBe(s.lastAllocatedOrdinal + 1n);
    const next = r.reduce(s, e).state; expect(next.branchContextEpoch).toBe(s.branchContextEpoch + 1n); expect(next.highestSeenGeneration).toBe('12'); assertClosed(next);
  });
  it.each(['legacy', 'closed', 'active'])('%s tagged target success enforces null matrix and does not cross target authority kind', async kind => {
    const r = await reducer(); let s = active(r);
    s = kind === 'closed' ? observe(r, s, { ...decision('12'), schemaVersion: 99 }) : kind === 'legacy' ? commit(r, s, observation(s, r.snapshotDecisionHeaders(new Headers(Object.fromEntries(legacy()))), 'admission_header')) : observe(r, s, decision('12', true));
    const token = structuredClone(s.pendingEffectPlan!); expect(token.target.targetKind).toBe(kind === 'legacy' ? 'legacy_restrictive_catalog' : kind === 'closed' ? 'closed_catalog' : 'active_catalog');
    if (kind !== 'active') expect(token.target.inputFingerprint).toBeNull();
    if (kind === 'legacy') expect(token.target.authorityDigest).toBeNull(); else expect(token.target.legacyProjectionDigest).toBeNull();
    token.target.targetKind = 'active_catalog'; token.target.inputFingerprint = 'forged'; token.target.authorityDigest = 'forged';
    const rejected = commit(r, s, { type: 'piEffectsSucceeded', effectPlanToken: token }); expect(rejected.pendingEffectPlan).toEqual(s.pendingEffectPlan); assertClosed(rejected);
    const finished = acknowledge(r, s); if (kind !== 'active') assertClosed(finished); else expect(finished.selectableModelIds).toEqual([F]);
  });
  it('wrong request context is discarded even if response generation is greater', async () => {
    const r = await reducer(); const s = active(r); const event = observation(s, decision('99')); event.requestControllerInstanceId = 'retired';
    const got = commit(r, s, event); expect(got.highestSeenGeneration).toBe('11'); expect(got.pendingEffectPlan).toBe(s.pendingEffectPlan); expect(got.appliedEffects).toEqual(s.appliedEffects);
    event.requestControllerInstanceId = s.controllerInstanceId; event.requestBranchContextEpoch = s.branchContextEpoch + 1n;
    expect(commit(r, s, event).highestSeenGeneration).toBe('11');
  });
  it('observation has exactly one close/install/emit return; preview is pure and deterministic', async () => {
    const r = await reducer(); const s = active(r); const before = structuredClone(s); const e = envelope(r, s, observation(s, decision('12', true)));
    const first = r.reduce(s, e); const second = r.reduce(s, structuredClone(e));
    expect(s).toEqual(before); expect(first).toEqual(second); assertClosed(first.state);
    expect(e.baseStateRevision).toBe(s.stateRevision); expect(first.state.stateRevision).toBe(s.stateRevision + 1n);
    expect(first.state.pendingEffectPlan).toEqual(e.allocation.kind === 'apply_pi_catalog' ? e.allocation.token : null);
    expect(first.state.pendingEffectPlan?.target.authorityDigest).toBe(digest('authority', { outcome: 'catalog', authority: decision('12', true).authority }));
    expect(first.state.pendingEffectPlan?.target.catalogDigest).toBe(digest('catalog', [models[2]]));
    const effects = first.effects.filter(effect => effect.type === 'applyPiCatalog'); expect(effects).toHaveLength(1);
    expect(effects[0]).toMatchObject({ token: first.state.pendingEffectPlan, orderedModels: [models[2]], selectionModelId: F });
  });
  it.each(allocationFaults)('current allocation fault %s preserves newly advanced denial but emits no attempt', async (_name, mutate) => {
    const r = await reducer(); const s = select(r, active(r), A); const e = envelope(r, s, observation(s, decision('12', true))); mutate(e);
    const got = r.reduce(s, e); expect(got.state.highestSeenGeneration).toBe('12'); expect(got.state.preRestrictionModelId).toBe(A); expect(got.state.appliedEffects.status).toBe('effect_failed_closed');
    expect(got.state.lastAllocatedOrdinal).toBe(s.lastAllocatedOrdinal); expect(got.state.pendingEffectPlan).toBeNull(); expect(got.effects.filter(x => x.type === 'applyPiCatalog')).toEqual([]); assertClosed(got.state);
    expect(got.effects).toEqual(expect.arrayContaining([
      expect.objectContaining({ type: 'diagnostic', stage: 'adapter_invariant', boundedErrorClass: 'effect_plan_protocol_error' }),
      expect.objectContaining({ type: 'requestReadback' }),
    ]));
  });
  it.each(['controllerInstanceId', 'baseStateRevision', 'branchContextEpoch'])('stale envelope %s cannot cancel a newer plan', async field => {
    const r = await reducer(); const s = observe(r, active(r), decision('12', true)); const e = envelope(r, s, { type: 'leaseTick' });
    Object.assign(e, { [field]: field === 'controllerInstanceId' ? 'foreign' : -1n });
    expect(r.reduce(s, e)).toEqual({ state: s, effects: [] });
  });
  it('replayed observation/retry envelope and duplicate completion are once-only', async () => {
    const r = await reducer(); const s = active(r); const e = envelope(r, s, observation(s, decision('12', true))); const pending = r.reduce(s, e).state;
    expect(r.reduce(pending, e)).toEqual({ state: pending, effects: [] });
    const failed = commit(r, pending, { type: 'piEffectsFailed', effectPlanToken: pending.pendingEffectPlan, stage: 'set_model', boundedErrorClass: 'fixture' });
    const retry = envelope(r, failed, { type: 'retryPiEffects', expectedTargetKey: failed.desiredTarget }); const attempt = r.reduce(failed, retry).state;
    expect(attempt.pendingEffectPlan?.planOrdinal).toBe(pending.lastAllocatedOrdinal + 1n); expect(attempt.pendingEffectPlan?.attemptOrdinal).toBe(attempt.pendingEffectPlan?.planOrdinal);
    expect(r.reduce(attempt, retry)).toEqual({ state: attempt, effects: [] });
    const done = { type: 'piEffectsSucceeded', effectPlanToken: attempt.pendingEffectPlan }; const applied = commit(r, attempt, done);
    const repeated = commit(r, applied, done); expect(repeated.appliedEffects).toEqual(applied.appliedEffects); expect(repeated.pendingEffectPlan).toBeNull(); expect(repeated.lastAllocatedOrdinal).toBe(applied.lastAllocatedOrdinal);
  });
  const tokenFields = ['controllerInstanceId', 'planOrdinal', 'attemptOrdinal', 'branchContextEpoch', 'targetKind', 'safetyGeneration', 'inputFingerprint', 'authorityDigest', 'legacyProjectionDigest', 'catalogDigest', 'selectionModelId'];
  it.each(tokenFields)('completion exactness: mutated %s cannot acknowledge', async field => {
    const r = await reducer(); const s = observe(r, active(r), decision('12', true)); const token = structuredClone(s.pendingEffectPlan!) as Token;
    const object = field in token ? token : token.target; const previous = Reflect.get(object, field) as unknown;
    Reflect.set(object, field, typeof previous === 'bigint' ? previous + 1n : typeof previous === 'string' ? `${previous}-suffix` : 'wrong');
    const got = commit(r, s, { type: 'piEffectsSucceeded', effectPlanToken: token }); assertClosed(got); expect(got.pendingEffectPlan).toEqual(s.pendingEffectPlan);
  });
  it.each(['piEffectsSucceeded', 'piEffectsFailed'])('superseded %s does not fail/open/cancel newer target or consume memory', async type => {
    const r = await reducer(); const old = observe(r, select(r, active(r), A), decision('12', true)); const current = observe(r, old, decision('13'));
    const got = commit(r, current, { type, effectPlanToken: old.pendingEffectPlan, stage: 'set_model', boundedErrorClass: 'fixture' });
    expect(got.pendingEffectPlan).toEqual(current.pendingEffectPlan); expect(got.preRestrictionModelId).toBe(current.preRestrictionModelId); expect(got.appliedEffects).toEqual(current.appliedEffects); expect(got.highestSeenGeneration).toBe('13');
  });
  it('mutating observation after preview cannot reuse the old target; unexpected allocation is failed closed', async () => {
    const r = await reducer(); const s = active(r); const event = observation(s, decision('12', true)); const e = envelope(r, s, event);
    (event.bundle as Decision).authority.inputFingerprint = 'mutated-after-preview';
    const got = r.reduce(s, e); expect(got.state.appliedEffects.status).toBe('effect_failed_closed'); expect(got.effects.filter(x => x.type === 'applyPiCatalog')).toEqual([]);
    const idle = envelope(r, s, { type: 'leaseTick' }); idle.allocation = e.allocation; assertClosed(r.reduce(s, idle).state);
  });
});

// Supplemental pure header tests. HTTP adapter witnesses are separate and mandatory.
describe('R5 bounded snapshot and denial-only legacy reduction', () => {
  it.each(['broken parent', 'duplicate ID', 'invalid ID', 'leaf mismatch'])('active path %s preserves validated prefix safety but clears preference', async fault => {
    const r = await reducer(); const p = await pinned(); const sm = p.SessionManager.inMemory(repo);
    sm.appendCustomEntry(customType, checkpoint(sm)); sm.appendCustomEntry('unrelated', {});
    const entries = structuredClone(sm.getBranch()); let leafId = sm.getLeafId();
    if (fault === 'broken parent') entries[1].parentId = 'not-prefix';
    if (fault === 'duplicate ID') entries[1].id = entries[0].id;
    if (fault === 'invalid ID') entries[1].id = '';
    if (fault === 'leaf mismatch') leafId = 'not-leaf';
    const s = commit(r, rawInitial(r), { type: 'activeBranchRestored', reason: 'startup', branchSnapshot: { sessionId: sm.getSessionId(), leafId, entries } }, 0n);
    expect(s.highestSeenGeneration).toBe('12'); expect(s.authorityStatus).toBe('quarantined'); expect(s.preRestrictionModelId).toBeNull(); assertClosed(s);
  });
  it('restored bound legacy pending keeps only floor/projection and accepts equal completion only by exact overlap', async () => {
    const r = await reducer(); const p = await pinned(); const sm = p.SessionManager.inMemory(repo);
    const record = checkpoint(sm); Object.assign(record.safety as object, { authorityStatus: 'legacy_restrictive_pending', legacyRestriction: { generation: '12', quotaState: 'fallback', policyRevision: '3', quotaEpisode: '1', restricted: true, effectiveModel: F, allowedCodexModelIds: [F] } });
    sm.appendCustomEntry(customType, record);
    const s = commit(r, rawInitial(r), { type: 'activeBranchRestored', reason: 'startup', branchSnapshot: { sessionId: sm.getSessionId(), leafId: sm.getLeafId(), entries: structuredClone(sm.getBranch()) } }, 0n);
    expect(s.highestSeenGeneration).toBe('12'); expect(s.authorityStatus).toBe('legacy_restrictive_pending'); expect(s.leaseDeadline).toBeNull(); expect(s.authoritativeRefreshRequired).toBe(true); assertClosed(s);
    expect(observe(r, s, decision('11')).highestSeenGeneration).toBe('12');
    const conflict = observe(r, s, decision('12')); expect(conflict.authorityStatus).toBe('quarantined'); assertClosed(conflict);
    const full = observe(r, s, decision('12', true)); expect(full.desiredTarget.targetKind).toBe('active_catalog'); assertClosed(full); expect(acknowledge(r, full).selectableModelIds).toEqual([F]);
  });
  it('snapshot captures each has/get once, retains no object, distinguishes absent/empty/over-limit/unreadable', async () => {
    const r = await reducer(); const calls: string[] = [];
    const h = { has(name: string): boolean { calls.push(`has:${name}`); if (name === headerNames[3]) throw new Error('fixture'); return name !== headerNames[0]; }, get(name: string): string | null { calls.push(`get:${name}`); return name === headerNames[0] ? null : name === headerNames[1] ? '' : name === headerNames[2] ? 'x'.repeat(21849) : 'x'; } };
    const result = r.snapshotDecisionHeaders(h); expect(Object.keys(result).sort()).toEqual([...headerNames].sort());
    expect(result[headerNames[0]]).toEqual({ kind: 'absent' }); expect(result[headerNames[1]]).toEqual({ kind: 'value', combined: '' }); expect(result[headerNames[2]]).toEqual({ kind: 'over_limit' }); expect(result[headerNames[3]]).toEqual({ kind: 'unreadable' });
    for (const name of headerNames) { expect(calls.filter(x => x === `has:${name}`)).toHaveLength(1); expect(calls.filter(x => x === `get:${name}`)).toHaveLength(1); }
    expect(Object.isFrozen(result)).toBe(true); expect(Object.values(result).every(Object.isFrozen)).toBe(true);
  });
  it.each(['active', 'legacy_restrictive_pending', 'authoritative_empty', 'quarantined', 'expired', 'effect_failed_closed', 'initial_closed'].flatMap(status => ['10', '11', '12'].map(g => [status, g])))('%s × legacy generation %s never grants or renews', async (status, generation) => {
    const r = await reducer(); let s = status === 'initial_closed' ? initial(r) : active(r);
    if (status === 'legacy_restrictive_pending') s = commit(r, s, observation(s, r.snapshotDecisionHeaders(new Headers(legacy('12').map(([k, v]): [string, string] => [k, v]))), 'admission_header'));
    if (status === 'authoritative_empty') s = observe(r, s, empty('12'));
    if (status === 'quarantined') s = observe(r, s, { ...decision('12'), schemaVersion: 99 });
    if (status === 'expired') s = commit(r, s, { type: 'leaseTick' }, 1000n * NS);
    if (status === 'effect_failed_closed') { s = observe(r, s, decision('12', true)); s = commit(r, s, { type: 'piEffectsFailed', effectPlanToken: s.pendingEffectPlan, stage: 'register_provider', boundedErrorClass: 'fixture' }); }
    const before = structuredClone(s);
    const got = commit(r, s, observation(s, r.snapshotDecisionHeaders(new Headers(legacy(generation).map(([k, v]): [string, string] => [k, v]))), 'admission_header'), status === 'expired' ? 1000n * NS : 12n * NS);
    expect(got.leaseDeadline).toBe(before.leaseDeadline);
    if (before.highestSeenGeneration !== null && BigInt(generation) < BigInt(before.highestSeenGeneration)) { expect(got.desiredTarget).toEqual(before.desiredTarget); expect(got.pendingEffectPlan).toEqual(before.pendingEffectPlan); expect(got.preRestrictionModelId).toBe(before.preRestrictionModelId); }
    else { assertClosed(got); expect(got.selectableModelIds).toEqual([]); }
  });
  it('readback and complete header codec agree; legacy success needs a fresh full-authority token', async () => {
    const r = await reducer(); const s = active(r); const d = decision('12', true);
    const header = observe(r, s, d); const viaHeader = commit(r, s, observation(s, r.snapshotDecisionHeaders(new Headers(encoded(d).map(([k, v]): [string, string] => [k, v]))), 'admission_header'));
    expect(viaHeader.acceptedAuthority).toEqual(header.acceptedAuthority); expect(viaHeader.desiredTarget).toEqual(header.desiredTarget);
    let pending = commit(r, s, observation(s, r.snapshotDecisionHeaders(new Headers(legacy().map(([k, v]): [string, string] => [k, v]))), 'admission_header'));
    const old = pending.pendingEffectPlan; expect(pending.authorityStatus).toBe('legacy_restrictive_pending'); expect(pending.desiredTarget.targetKind).toBe('legacy_restrictive_catalog');
    expect(old?.target.legacyProjectionDigest).toBe(digest('legacy', { generation: '12', quotaState: 'fallback', policyRevision: '3', quotaEpisode: '1', restricted: true, effectiveModel: F, allowedCodexModelIds: [F] }));
    expect(old?.target.catalogDigest).toBe(digest('catalog', [models[2]]));
    pending = acknowledge(r, pending); assertClosed(pending); expect(pending.authoritativeRefreshRequired).toBe(true);
    const full = commit(r, pending, observation(pending, d, 'readback', 13n * NS, 14n * NS), 14n * NS); expect(full.pendingEffectPlan).not.toEqual(old); expect(full.desiredTarget.targetKind).toBe('active_catalog'); assertClosed(full);
    expect(acknowledge(r, full, 14n * NS).selectableModelIds).toEqual([F]);
  });
});

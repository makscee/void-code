import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, beforeAll, describe, expect, it } from 'vitest';
import { A, B, assertClosed, assertPins, blocked, checkpoint, configurePinFetch, customType, decision, models, pinned, pinRig, productRig, replacementRuntime, repo } from './fixtures/pi-model-decision';
import type { ProductRig } from './fixtures/pi-model-decision';

beforeAll(async () => { assertPins(); await configurePinFetch(); }, 60_000);
const rigs: ProductRig[] = [];
const directories: string[] = [];
afterEach(async () => {
  for (const rig of rigs.splice(0).reverse()) await rig.shutdown();
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true });
});

// Actual pin controls are independent of every missing product export.
describe('pinned branch fixture controls', () => {
  it.each(['before', 'at'] as const)('actual AgentSessionRuntime.fork position=%s rebinds real session_start and keeps copied proof foreign', async position => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo);
    const proofId = sm.appendCustomEntry(customType, checkpoint(sm));
    const user = sm.appendMessage({ role: 'user', content: 'fixture', timestamp: 0 });
    const oldId = sm.getSessionId(); const starts: unknown[] = [];
    const first = await pinRig(pi => { pi.on('session_start', e => { starts.push(e); }); }, sm);
    let latest = first;
    const runtime = await replacementRuntime(first, async (manager, event) => {
      latest = await pinRig(pi => { pi.on('session_start', e => { starts.push(e); }); }, manager, event); return latest;
    });
    expect((await runtime.fork(user, { position })).cancelled).toBe(false);
    expect(runtime.session.sessionManager.getSessionId()).not.toBe(oldId);
    expect(starts).toEqual([{ type: 'session_start', reason: 'startup' }, { type: 'session_start', reason: 'fork', previousSessionFile: undefined }]);
    expect(latest.sm.getEntry(proofId)).toMatchObject({ data: { branchProof: { sessionId: oldId } } });
    await runtime.dispose();
  });
  it('getBranch is root→leaf while file-later sibling stays in getEntries only', async () => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo);
    const root = sm.appendCustomEntry('unrelated', {});
    const a = sm.appendCustomEntry(customType, checkpoint(sm, '12'));
    sm.branch(root); const b = sm.appendCustomEntry(customType, checkpoint(sm, '9', 'active', { preRestrictionModelId: B }));
    sm.branch(a); const poison = sm.appendCustomEntry(customType, checkpoint(sm, '99')); sm.branch(b);
    expect(sm.getEntries().map(e => e.id)).toEqual([root, a, b, poison]); expect(sm.getBranch().map(e => e.id)).toEqual([root, b]);
  });

  it('actual createBranchedSession changes header and rechains removed labels without rewriting payload proof', async () => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo);
    const root = sm.appendCustomEntry('fixture', {}); const label = sm.appendLabelChange(root, 'fixture-label');
    const proof = checkpoint(sm); const id = sm.appendCustomEntry(customType, proof); const oldSession = sm.getSessionId();
    expect((proof.branchProof as Record<string, unknown>).parentEntryId).toBe(label);
    expect(sm.createBranchedSession(id)).toBeUndefined();
    expect(sm.getSessionId()).not.toBe(oldSession); expect(sm.getEntry(id)?.parentId).toBe(root);
    expect(sm.getEntry(id)).toMatchObject({ data: proof });
    expect((proof.branchProof as Record<string, unknown>).sessionId).toBe(oldSession);
    expect(sm.getBranch().map(e => e.type)).toEqual(['custom', 'custom', 'label']);
  });

  it('real resume retains session ID and active-path record order from isolated JSONL', async () => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo); sm.appendCustomEntry(customType, checkpoint(sm));
    const directory = mkdtempSync(path.join(os.tmpdir(), 'vc-model-branch-')); directories.push(directory);
    const file = path.join(directory, 'fixture.jsonl'); writeFileSync(file, [sm.getHeader(), ...sm.getEntries()].map(entry => JSON.stringify(entry)).join('\n') + '\n');
    const resumed = p.SessionManager.open(file, directory);
    expect(resumed.getSessionId()).toBe(sm.getSessionId()); expect(resumed.getBranch()).toEqual(sm.getBranch());
  });

  it('real navigateTree emits session_tree after leaf change, not a fabricated fixture lifecycle event', async () => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo);
    const root = sm.appendCustomEntry('root', {}); sm.appendCustomEntry('a', {}); sm.branch(root); const b = sm.appendCustomEntry('b', {});
    const events: unknown[] = [];
    const rig = await pinRig(pi => { pi.on('session_tree', (e, ctx) => { events.push({ e, leaf: ctx.sessionManager.getLeafId() }); }); }, sm);
    await rig.session.navigateTree(root, { summarize: false });
    expect(events).toHaveLength(1); expect(events[0]).toMatchObject({ e: { type: 'session_tree', newLeafId: root }, leaf: root });
    expect(sm.getEntries().map(e => e.id)).toContain(b); rig.close();
  });
});

const corruptions: [string, (record: Record<string, unknown>) => void][] = [
  ['bad preference', r => { r.preference = { preRestrictionModelId: 99 }; }],
  ['missing safety', r => { delete r.safety; }],
  ['unknown version', r => { r.schemaVersion = 2; }],
  ['string version', r => { r.schemaVersion = '1'; }],
  ['bad bigint', r => { Object.assign(r.safety as object, { highestSeenGeneration: '01' }); }],
  ['unknown outer key', r => { r.appliedEffects = 'applied'; }],
  ['unknown safety key', r => { Object.assign(r.safety as object, { leaseDeadline: '99999999' }); }],
  ['invalid null tuple', r => { Object.assign(r.safety as object, { authorityStatus: 'active' }); }],
  ['oversized payload', r => { r.preference = { preRestrictionModelId: 'x'.repeat(32769) }; }],
  ['wrong session', r => { Object.assign(r.branchProof as object, { sessionId: 'foreign-session' }); }],
  ['wrong parent', r => { Object.assign(r.branchProof as object, { parentEntryId: 'foreign-parent' }); }],
  ['bad proof', r => { r.branchProof = null; }],
];

function fixtureSessionFile(sm: Parameters<typeof checkpoint>[0]): string {
  const directory = mkdtempSync(path.join(os.tmpdir(), 'vc-model-resume-')); directories.push(directory);
  const file = path.join(directory, 'fixture.jsonl');
  writeFileSync(file, [sm.getHeader(), ...sm.getEntries()].map(entry => JSON.stringify(entry)).join('\n') + '\n');
  return file;
}

async function assertRestoredClosed(rig: ProductRig, generation = '12'): Promise<void> {
  const state = rig.controller().snapshot(); expect(state.highestSeenGeneration).toBe(generation); expect(state.authorityStatus).toBe('quarantined');
  expect(state.leaseDeadline).toBeNull(); expect(state.appliedEffects.status).not.toBe('applied'); expect(state.preRestrictionModelId).toBeNull(); assertClosed(state);
  await blocked(rig, [models[0] as Parameters<typeof rig.stream>[0]]);
}

describe('R2 F2 / R5 product active-branch monotonic reconstruction', () => {
  it('actual checkpoint writer normalizes quarantine and persists no token, lease deadline, URL or credential', async () => {
    const rig = await productRig({ startup: decision('11') }); rigs.push(rig);
    await rig.readback({ ...decision('12'), schemaVersion: 99 });
    const entry = rig.sm.getBranch().filter(e => e.type === 'custom' && e.customType === customType).at(-1)!;
    expect(entry).toBeDefined(); if (entry.type !== 'custom') throw new Error('missing checkpoint');
    const data = entry.data as Record<string, unknown>;
    expect(Object.keys(data).sort()).toEqual(['branchProof', 'preference', 'safety', 'schemaVersion']);
    expect(data.schemaVersion).toBe(1);
    expect(data.branchProof).toEqual({ sessionId: rig.sm.getSessionId(), parentEntryId: entry.parentId, writeReason: 'safety_commit' });
    expect(data.safety).toEqual({ highestSeenGeneration: '12', authorityStatus: 'quarantined', acceptedAuthority: null, lastLeaseEvidence: null, legacyRestriction: null });
    expect(Object.keys(data.preference as object)).toEqual(['preRestrictionModelId']);
    const serialized = JSON.stringify(data); expect(serialized).not.toContain('fixture-not-a-credential'); expect(serialized).not.toContain('fixture.invalid'); expect(serialized).not.toContain('leaseDeadline'); expect(serialized).not.toContain('appliedEffects'); expect(serialized).not.toContain('planOrdinal');
  });
  it.each(['before', 'at'] as const)('runtime fork/clone position=%s cannot restore copied authority/preference or lower floor', async position => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo);
    const root = sm.appendCustomEntry('root', {}); sm.appendLabelChange(root, 'fixture-label');
    sm.appendCustomEntry(customType, checkpoint(sm, '12', 'active'));
    const user = sm.appendMessage({ role: 'user', content: 'fixture', timestamp: 0 });
    const first = await productRig({ sm }); rigs.push(first); const old = first.controller().snapshot();
    let next = first;
    const runtime = await replacementRuntime(first, async (manager, event) => { next = await productRig({ sm: manager, reason: event }); rigs.push(next); return next; });
    expect((await runtime.fork(user, { position })).cancelled).toBe(false);
    expect(next.controller().snapshot().controllerInstanceId).not.toBe(old.controllerInstanceId);
    await assertRestoredClosed(next);
    await next.respondReadback(decision('11')); await next.controller().whenIdle(); await assertRestoredClosed(next);
  });

  it('runtime new discards session-local preference and authority; fork before all proof cannot scan source history', async () => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo);
    const firstUser = sm.appendMessage({ role: 'user', content: 'fixture', timestamp: 0 }); sm.appendCustomEntry(customType, checkpoint(sm));
    const first = await productRig({ sm }); rigs.push(first); let next = first;
    const runtime = await replacementRuntime(first, async (manager, event) => { next = await productRig({ sm: manager, reason: event }); rigs.push(next); return next; });
    await runtime.fork(firstUser, { position: 'before' });
    expect(next.controller().snapshot().highestSeenGeneration).toBeNull(); expect(next.controller().snapshot().preRestrictionModelId).toBeNull(); assertClosed(next.controller().snapshot());
    await runtime.newSession(); expect(next.controller().snapshot().highestSeenGeneration).toBeNull(); assertClosed(next.controller().snapshot());
  });
  it.each(corruptions)('quarantine 12 + corrupt newest %s cannot reset floor on real restart', async (_name, corrupt) => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo);
    sm.appendCustomEntry(customType, checkpoint(sm)); const bad = checkpoint(sm, '12'); corrupt(bad); sm.appendCustomEntry(customType, bad);
    const file = fixtureSessionFile(sm);
    const first = await productRig(); rigs.push(first); let rig = first;
    const runtime = await replacementRuntime(first, async (manager, event) => { rig = await productRig({ sm: manager, reason: event }); rigs.push(rig); return rig; });
    await runtime.switchSession(file); await assertRestoredClosed(rig);
    // The actual startup readback response, not a synthetic reducer observation.
    await rig.respondReadback(decision('11')); await rig.controller().whenIdle();
    await assertRestoredClosed(rig);
    await rig.readback(decision('12')); await assertRestoredClosed(rig);
    await rig.readback(decision('13')); expect(rig.controller().snapshot().selectableModelIds).toEqual(models.map(m => m.id));
  });

  it('valid lower-9 latest preference B may restore preference but cannot lower quarantine 12', async () => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo); sm.appendCustomEntry(customType, checkpoint(sm));
    sm.appendCustomEntry(customType, checkpoint(sm, '9', 'active', { preRestrictionModelId: B }));
    const rig = await productRig({ sm, reason: 'resume' }); rigs.push(rig);
    expect(rig.controller().snapshot()).toMatchObject({ highestSeenGeneration: '12', authorityStatus: 'quarantined', preRestrictionModelId: B, leaseDeadline: null });
    await blocked(rig, [models[0] as Parameters<typeof rig.stream>[0]]);
  });

  it('safety validation is independent of bad preference; valid active 12 then quarantine 13 plus invalid suffix retains 13', async () => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo);
    sm.appendCustomEntry(customType, checkpoint(sm, '12', 'active', { preRestrictionModelId: [] }));
    sm.appendCustomEntry(customType, checkpoint(sm, '13')); sm.appendCustomEntry(customType, { schemaVersion: 999, generation: '1' });
    const rig = await productRig({ sm }); rigs.push(rig); await assertRestoredClosed(rig, '13');
  });

  it.each(['authority', 'evidence', 'later active', 'initial_closed'])('equal-generation %s cannot clear quarantine or choose more permissive history', async kind => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo);
    sm.appendCustomEntry(customType, checkpoint(sm, '12', kind === 'authority' || kind === 'evidence' ? 'active' : 'quarantined'));
    const later = checkpoint(sm, '12', 'active');
    const safety = later.safety as Record<string, unknown>;
    if (kind === 'authority') (safety.acceptedAuthority as Record<string, unknown>).inputFingerprint = 'conflict';
    if (kind === 'evidence') (safety.lastLeaseEvidence as Record<string, unknown>).validUntil = '2026-09-16T12:01:59.000000000Z';
    if (kind === 'initial_closed') Object.assign(safety, { highestSeenGeneration: null, authorityStatus: 'initial_closed', acceptedAuthority: null, lastLeaseEvidence: null });
    sm.appendCustomEntry(customType, later);
    const rig = await productRig({ sm }); rigs.push(rig); expect(rig.controller().snapshot().highestSeenGeneration).toBe('12'); expect(rig.controller().snapshot().authorityStatus).toBe('quarantined'); assertClosed(rig.controller().snapshot());
  });

  it('greater valid historical recovery folds but cannot restore a deadline/applied gate or renew exact persisted evidence', async () => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo); sm.appendCustomEntry(customType, checkpoint(sm, '13')); sm.appendCustomEntry(customType, checkpoint(sm, '14', 'active'));
    const rig = await productRig({ sm }); rigs.push(rig);
    expect(rig.controller().snapshot().highestSeenGeneration).toBe('14'); expect(rig.controller().snapshot().leaseDeadline).toBeNull(); assertClosed(rig.controller().snapshot());
    await rig.respondReadback(decision('14')); await rig.controller().whenIdle(); assertClosed(rig.controller().snapshot());
    const renewed = decision('14'); renewed.evaluatedAt = '2026-09-16T12:01:00.000000000Z'; renewed.validUntil = '2026-09-16T12:03:00.000000000Z';
    await rig.readback(renewed); expect(rig.controller().snapshot().selectableModelIds).toEqual(models.map(m => m.id));
  });

  it('real loaded active path follows parents, not inverted timestamps or lexical entry IDs', async () => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo); sm.appendCustomEntry(customType, checkpoint(sm)); sm.appendCustomEntry(customType, checkpoint(sm, '9', 'active', { preRestrictionModelId: B }));
    const entries = structuredClone(sm.getBranch()); entries[0].id = 'z-root'; entries[0].timestamp = '2030-01-01T00:00:00.000Z'; entries[1].id = 'a-leaf'; entries[1].parentId = 'z-root'; entries[1].timestamp = '2000-01-01T00:00:00.000Z';
    if (entries[1].type !== 'custom') throw new Error('fixture must be a custom entry');
    (entries[1].data as { branchProof: { parentEntryId: string } }).branchProof.parentEntryId = 'z-root';
    const directory = mkdtempSync(path.join(os.tmpdir(), 'vc-model-order-')); directories.push(directory); const file = path.join(directory, 'fixture.jsonl');
    writeFileSync(file, [sm.getHeader(), ...entries].map(entry => JSON.stringify(entry)).join('\n') + '\n');
    const restored = p.SessionManager.open(file, directory); expect(restored.getBranch().map(e => e.id)).toEqual(['z-root', 'a-leaf']);
    const rig = await productRig({ sm: restored, reason: 'resume' }); rigs.push(rig);
    expect(rig.controller().snapshot()).toMatchObject({ highestSeenGeneration: '12', authorityStatus: 'quarantined', preRestrictionModelId: B, leaseDeadline: null }); assertClosed(rig.controller().snapshot());
  });

  it('restart on branch B with no on-path proof cannot import sibling A quarantine or preference', async () => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo); const root = sm.appendCustomEntry('root', {});
    const sibling = sm.appendCustomEntry(customType, checkpoint(sm)); sm.branch(root); sm.appendMessage({ role: 'user', content: 'fixture B', timestamp: 0 });
    expect(sm.getEntries().map(e => e.id)).toContain(sibling); expect(sm.getBranch().map(e => e.id)).not.toContain(sibling);
    const rig = await productRig({ sm }); rigs.push(rig); expect(rig.controller().snapshot().highestSeenGeneration).toBeNull(); expect(rig.controller().snapshot().preRestrictionModelId).toBeNull(); assertClosed(rig.controller().snapshot());
    await rig.respondReadback(decision('11')); await rig.controller().whenIdle(); expect(rig.controller().snapshot().selectableModelIds).toEqual(models.map(m => m.id));
  });

  it.each(['high valid poison', 'corrupt poison'])('abandoned branch %s never contaminates active-path fold/preference', async kind => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo); const root = sm.appendCustomEntry('fixture', {});
    const current = sm.appendCustomEntry(customType, checkpoint(sm, '12'));
    sm.branch(root); sm.appendCustomEntry(customType, kind === 'high valid poison' ? checkpoint(sm, '99', 'active', { preRestrictionModelId: B }) : { schemaVersion: 1 }); sm.branch(current);
    const rig = await productRig({ sm }); rigs.push(rig); expect(rig.controller().snapshot().highestSeenGeneration).toBe('12'); expect(rig.controller().snapshot().preRestrictionModelId).toBe(A);
  });

  it('real tree navigation preserves in-memory quarantine, increments epoch and appends current-bound rebase before readback', async () => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo); const root = sm.appendCustomEntry('fixture', {});
    const a = sm.appendCustomEntry(customType, checkpoint(sm, '12')); sm.branch(root);
    const b = sm.appendCustomEntry(customType, checkpoint(sm, '9', 'active', { preRestrictionModelId: B })); sm.branch(a);
    const rig = await productRig({ sm }); rigs.push(rig); const before = rig.controller().snapshot();
    await rig.session.navigateTree(b, { summarize: false });
    const after = rig.controller().snapshot(); expect(after.highestSeenGeneration).toBe('12'); expect(after.authorityStatus).toBe('quarantined'); expect(after.preRestrictionModelId).toBe(B); expect(after.branchContextEpoch).toBe(before.branchContextEpoch + 1n); assertClosed(after);
    const entries = sm.getBranch(); const rebase = entries.filter(e => e.type === 'custom' && e.customType === customType).at(-1)!;
    expect(rebase).toMatchObject({ data: { branchProof: { sessionId: sm.getSessionId(), parentEntryId: rebase.parentId, writeReason: 'branch_rebase' }, safety: { highestSeenGeneration: '12' } } });
    await blocked(rig, [models[0] as Parameters<typeof rig.stream>[0]]);
    const bad = checkpoint(sm); bad.preference = { preRestrictionModelId: [] }; sm.appendCustomEntry(customType, bad);
    const file = fixtureSessionFile(sm); let resumed = rig;
    const runtime = await replacementRuntime(rig, async (manager, event) => { resumed = await productRig({ sm: manager, reason: event }); rigs.push(resumed); return resumed; });
    await runtime.switchSession(file); await assertRestoredClosed(resumed);
  });

  it.each(['quarantined', 'active'])('real fork with label rechain preserves %s 12 as denial-only, not bound authority/preference', async status => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo); const root = sm.appendCustomEntry('fixture', {}); sm.appendLabelChange(root, 'fixture');
    const id = sm.appendCustomEntry(customType, checkpoint(sm, '12', status)); const old = sm.getSessionId(); sm.createBranchedSession(id);
    expect(sm.getSessionId()).not.toBe(old);
    const rig = await productRig({ sm, reason: 'fork' }); rigs.push(rig); await assertRestoredClosed(rig);
    await rig.respondReadback(decision('11')); await rig.controller().whenIdle(); await assertRestoredClosed(rig);
  });

  it.each(['startup', 'resume', 'new', 'fork', 'reload'] as const)('%s with genuinely absent on-path proof starts empty/refetch, not sibling recovery', async reason => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo);
    const rig = await productRig({ sm, reason }); rigs.push(rig);
    expect(rig.controller().snapshot().highestSeenGeneration).toBeNull(); expect(rig.controller().snapshot().preRestrictionModelId).toBeNull(); expect(rig.controller().snapshot().leaseDeadline).toBeNull(); assertClosed(rig.controller().snapshot());
    expect(rig.http.posts).toBe(0); await blocked(rig, [models[0] as Parameters<typeof rig.stream>[0]]);
  });

  it('valid latest preference absent from fresh catalog cannot select or execute that model after restart', async () => {
    const p = await pinned(); const sm = p.SessionManager.inMemory(repo); sm.appendCustomEntry(customType, checkpoint(sm, '12', 'quarantined', { preRestrictionModelId: A }));
    const rig = await productRig({ sm }); rigs.push(rig); expect(rig.controller().snapshot().preRestrictionModelId).toBe(A);
    const d = decision('13'); d.authority.allowedCodexModelIds = [B, models[2].id]; await rig.respondReadback(d); await rig.controller().whenIdle();
    expect(rig.session.model?.id).toBe(B); expect(rig.controller().snapshot().selectableModelIds).toEqual(d.authority.allowedCodexModelIds); await blocked(rig, [models[0]]);
  });

  it('actual reload replaces controller identity and cannot import applied state from old instance', async () => {
    const rig = await productRig({ startup: decision() }); rigs.push(rig); const old = rig.controller().snapshot();
    await rig.session.reload(); const next = rig.controller().snapshot(); expect(next.controllerInstanceId).not.toBe(old.controllerInstanceId); expect(next.leaseDeadline).toBeNull(); assertClosed(next);
    await blocked(rig, [models[0] as Parameters<typeof rig.stream>[0]]);
  });
});

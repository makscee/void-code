import { afterEach, beforeAll, describe, expect, it } from 'vitest';
import { A, B, F, NS, assertClosed, assertPins, blocked, bootstrap, configurePinFetch, controlConfig, decision, deferred, encoded, legacy, models, pinRig, productRig, provider, receiveHeaders, registryIds, requiredModel } from './fixtures/pi-model-decision';
import type { Fault, ProductRig } from './fixtures/pi-model-decision';

beforeAll(async () => { assertPins(); await configurePinFetch(); }, 60_000);

describe('pinned registry fixture controls — actual bound ExtensionAPI and AgentSession', () => {
  it('post-start replacement removes rows but leaves removed current object; real setModel emits awaited model_select', async () => {
    const rig = await pinRig();
    rig.pi.registerProvider(provider, controlConfig());
    const old = requiredModel(rig, A);
    expect(await rig.pi.setModel(old)).toBe(true);
    rig.pi.registerProvider(provider, controlConfig([F]));
    expect(registryIds(rig)).toEqual([F]);
    expect((await rig.runtime.getAvailable(provider)).map(m => m.id)).toEqual([F]);
    expect(rig.registry.getAvailable().filter(m => m.provider === provider).map(m => m.id)).toEqual([F]);
    expect(rig.session.model).toBe(old);
    const fresh = requiredModel(rig, F);
    expect(await rig.pi.setModel(fresh)).toBe(true);
    expect(rig.session.model).toBe(fresh);
    expect(rig.selections).toHaveLength(2);
    rig.pi.registerProvider(provider, controlConfig([]));
    expect(registryIds(rig)).toEqual([]);
    expect(rig.session.model).toBe(fresh);
    expect(rig.registry.find(provider, F)).toBeUndefined();
    rig.close();
  });

  it('native widening re-registration replaces the surviving object without changing logical selection', async () => {
    const rig = await pinRig();
    rig.pi.registerProvider(provider, controlConfig([F]));
    const old = requiredModel(rig, F);
    expect(await rig.pi.setModel(old)).toBe(true);
    const selectionCount = rig.selections.length;
    rig.pi.registerProvider(provider, controlConfig([F, A]));
    const fresh = requiredModel(rig, F);
    expect(fresh).not.toBe(old);
    expect(rig.session.model).not.toBe(old);
    expect(rig.session.model).toMatchObject({ provider, id: F });
    expect(rig.selections).toHaveLength(selectionCount);
    rig.close();
  });

  it('native invalid registration throws synchronously before replacing old config or selected object', async () => {
    const rig = await pinRig();
    rig.pi.registerProvider(provider, controlConfig());
    const model = requiredModel(rig, A);
    await rig.pi.setModel(model);
    const before = rig.runtime.getRegisteredProviderConfig(provider);
    expect(() => rig.pi.registerProvider(provider, { ...controlConfig([F]), api: undefined })).toThrow();
    expect(rig.runtime.getRegisteredProviderConfig(provider)).toBe(before);
    expect(registryIds(rig)).toEqual([A, 'fixture-default', F]);
    expect(rig.session.model).toBe(model);
    rig.close();
  });

  it('native configured-auth false returns false before AgentSession selection', async () => {
    const rig = await pinRig();
    rig.pi.registerProvider(provider, controlConfig());
    const old = requiredModel(rig, A); await rig.pi.setModel(old);
    const original = rig.runtime.hasConfiguredAuth;
    rig.runtime.hasConfiguredAuth = () => false;
    expect(await rig.pi.setModel(requiredModel(rig, F))).toBe(false);
    rig.runtime.hasConfiguredAuth = original;
    expect(rig.session.model).toBe(old);
    expect(rig.selections).toHaveLength(1);
    rig.close();
  });

  it('native configured-auth true plus failed checkAuth rejects before selection', async () => {
    const rig = await pinRig();
    rig.pi.registerProvider(provider, controlConfig());
    const old = requiredModel(rig, A); await rig.pi.setModel(old);
    expect(rig.runtime.hasConfiguredAuth(provider)).toBe(true);
    const original = rig.runtime.checkAuth;
    rig.runtime.checkAuth = async () => undefined;
    await expect(rig.pi.setModel(requiredModel(rig, F))).rejects.toThrow('No API key');
    rig.runtime.checkAuth = original;
    expect(rig.session.model).toBe(old);
    expect(rig.selections).toHaveLength(1);
    rig.close();
  });

  it('appendEntry returns void and binds the actual parent; getBranch excludes siblings', async () => {
    const rig = await pinRig();
    expect(rig.pi.appendEntry('fixture', { n: 1 })).toBeUndefined();
    const root = rig.sm.getLeafId()!;
    const prefix = rig.sm.getBranch().map(e => e.id);
    rig.pi.appendEntry('fixture', { n: 2 }); const sibling = rig.sm.getLeafId();
    rig.sm.branch(root);
    rig.pi.appendEntry('fixture', { n: 3 });
    expect(rig.sm.getLeafEntry()?.parentId).toBe(root);
    expect(rig.sm.getBranch().map(e => e.id)).toEqual([...prefix, rig.sm.getLeafId()]);
    expect(rig.sm.getEntries().map(e => e.id)).toContain(sibling);
    rig.close();
  });
});

const rigs: ProductRig[] = [];
afterEach(async () => { for (const rig of rigs.splice(0).reverse()) await rig.shutdown(); });
const faults: Fault[] = ['register_invalid', 'false', 'throw', 'reject_before', 'reject_after', 'native_false', 'native_reject'];

describe('R2/R3/F3 product failure witnesses — real Pi partial effects and actual stream', () => {
  it('server order replaces local-ceiling order while preserving real provider transport configuration', async () => {
    const rig = await productRig({ startup: decision('11') }); rigs.push(rig);
    const d = decision('12'); d.authority.allowedCodexModelIds = [F, A, B]; d.authority.defaultCodexModelId = d.authority.effectiveCodexModelId = A;
    await rig.readback(d);
    expect(registryIds(rig)).toEqual([F, A, B]); expect(rig.controller().snapshot().selectableModelIds).toEqual([F, A, B]); expect(rig.session.model?.id).toBe(A);
    expect(rig.runtime.getRegisteredProviderConfig(provider)).toMatchObject({ baseUrl: bootstrap.relayUrl, apiKey: bootstrap.authToken, api: 'void-codex-sse', headers: { 'x-void-provider': 'fixture-route' }, streamSimple: rig.product.streamVoidCodex });
    expect(rig.boundary.calls.filter(c => c.method === 'register').at(-1)?.ids).toEqual([F, A, B]);
  });
  it('T1 removal of a stale actual Astra ID uses server default without remembering or silently regranting Astra', async () => {
    // Fictional server decisions, not a tier catalog or entitlement assignment.
    const astra = 'gpt-6-astra'; const ceiling = models.map(m => m.id === A ? { ...m, id: astra, name: 'Fixture Astra' } : m);
    const start = decision('11'); start.authority.allowedCodexModelIds = [astra, B, F];
    const rig = await productRig({ startup: start, compatibility: ceiling, bootstrapValue: { ...bootstrap, providers: [{ ...bootstrap.providers[0], models: [astra, B, F] }] } }); rigs.push(rig);
    const stale = requiredModel(rig, astra); await rig.pi.setModel(stale);
    const narrow = decision('12'); narrow.authority.allowedCodexModelIds = [B, F]; await rig.readback(narrow);
    expect(registryIds(rig)).toEqual([B, F]); expect(rig.session.model?.id).toBe(B); expect(rig.controller().snapshot().preRestrictionModelId).toBeNull();
    await blocked(rig, [stale]);
    const expanded = decision('13'); expanded.authority.allowedCodexModelIds = [astra, B, F]; await rig.readback(expanded);
    expect(rig.session.model?.id).toBe(B); expect(rig.controller().snapshot().preRestrictionModelId).toBeNull();
  });
  it.each(['set', 'cycle'] as const)('real own model_select is provisional; interleaved user %s cannot overwrite memory or deadlock worker', async action => {
    const rig = await productRig({ startup: decision('11') }); rigs.push(rig); await rig.pi.setModel(requiredModel(rig, A)); await rig.readback(decision('12', true));
    const stale = rig.session.model!; rig.boundary.entered = deferred<void>(); rig.boundary.holdSet = deferred<void>();
    const response = await receiveHeaders(rig, encoded(decision('13'))); await rig.boundary.entered.promise;
    const pending = rig.controller().snapshot(); expect(pending.preRestrictionModelId).toBe(A); assertClosed(pending);
    const own = rig.trace.filter(t => t.kind === 'event-ingress' && t.event?.type === 'localModelSelected' && t.event.ownEffectToken !== null).at(-1);
    expect(own?.event).toMatchObject({ ownEffectToken: pending.pendingEffectPlan, modelId: A, providerId: provider });
    if (action === 'set') await rig.pi.setModel(requiredModel(rig, B)); else await rig.session.cycleModel('forward');
    expect(rig.controller().snapshot().preRestrictionModelId).toBe(A); assertClosed(rig.controller().snapshot());
    await blocked(rig, [stale, requiredModel(rig, B)]);
    rig.boundary.holdSet.resolve(); rig.boundary.holdSet = null; await rig.controller().whenIdle();
    expect(rig.session.model?.id).toBe(A); expect(rig.controller().snapshot().preRestrictionModelId).toBeNull(); expect(rig.boundary.maximumActive).toBe(1); await response.release();
  });

  it.each(['success', 'failure'] as const)('real old-epoch %s cannot cross navigateTree rebase; fresh request context must reapply', async outcome => {
    const rig = await productRig({ startup: decision('11') }); rigs.push(rig); await rig.pi.setModel(requiredModel(rig, A));
    const root = rig.sm.getBranch()[0].id; rig.boundary.entered = deferred<void>(); rig.boundary.holdSet = deferred<void>();
    if (outcome === 'failure') rig.boundary.fault = 'reject_after';
    const response = await receiveHeaders(rig, encoded(decision('12', true))); await rig.boundary.entered.promise;
    const old = rig.controller().snapshot(); await rig.session.navigateTree(root, { summarize: false });
    const rebased = rig.controller().snapshot(); expect(rebased.branchContextEpoch).toBe(old.branchContextEpoch + 1n); expect(rebased.highestSeenGeneration).toBe('12'); assertClosed(rebased);
    rig.boundary.holdSet.resolve(); rig.boundary.holdSet = null; await rig.controller().whenIdle(); assertClosed(rig.controller().snapshot());
    expect(rig.controller().snapshot().branchContextEpoch).toBe(rebased.branchContextEpoch); await blocked(rig, [models[0], models[2]]);
    const d = decision('13', true); await rig.respondReadback(d); await rig.controller().whenIdle(); expect(rig.controller().snapshot().selectableModelIds).toEqual([F]); await response.release();
  });
  it.each(faults.flatMap(fault => ['restrictive', 'widening', 'legacy'].map(direction => ({ fault, direction }))))('$direction / $fault preserves safety, closes both objects, emits exactly one failure, retries complete new-token sequence', async ({ fault, direction }) => {
    const rig = await productRig({ startup: decision('10') }); rigs.push(rig);
    await rig.pi.setModel(requiredModel(rig, A));
    if (direction === 'widening') await rig.readback(decision('11', true));
    const stale = rig.session.model!;
    const before = rig.controller().snapshot();
    const start = rig.trace.length;
    const callStart = rig.boundary.calls.length;
    rig.boundary.fault = fault;
    // All failures originate inside the actual product worker; fixture submits no
    // success/failure event and never writes its snapshot, registry or model.
    const d = decision('12', direction !== 'widening');
    if (direction === 'widening') Object.assign(d.authority, { controlMode: 'warn', controlRevision: '9', controlEpoch: '10', quotaState: 'fallback', quotaEpisode: '1' });
    const response = await receiveHeaders(rig, direction === 'legacy' ? legacy() : encoded(d));
    await rig.controller().whenIdle();
    const after = rig.controller().snapshot();
    expect(after.highestSeenGeneration).toBe('12'); expect(after.branchContextEpoch).toBe(before.branchContextEpoch);
    expect(after.appliedEffects.status).toBe('effect_failed_closed'); expect(after.pendingEffectPlan).toBeNull(); assertClosed(after);
    const failureEvents = rig.trace.slice(start).filter(t => t.kind === 'event-ingress' && t.event?.type === 'piEffectsFailed');
    expect(failureEvents).toHaveLength(1);
    expect(failureEvents[0].event?.stage).toBe(fault === 'register_invalid' ? 'register_provider' : 'set_model');
    expect(typeof failureEvents[0].event?.boundedErrorClass).toBe('string');
    expect(String(failureEvents[0].event?.boundedErrorClass)).not.toContain('fixture-not-a-credential');
    expect(String(failureEvents[0].event?.boundedErrorClass).length).toBeLessThanOrEqual(256);
    const token = failureEvents[0].event?.effectPlanToken;
    expect(token).toEqual(rig.trace.slice(start).find(t => t.kind === 'commit' && t.envelope?.event.type === 'observeDecision')?.envelope?.allocation.kind === 'apply_pi_catalog'
      ? (rig.trace.slice(start).find(t => t.kind === 'commit' && t.envelope?.event.type === 'observeDecision')!.envelope!.allocation as { token: unknown }).token : undefined);
    expect(rig.trace.slice(start).filter(t => t.kind === 'event-ingress' && t.event?.type === 'piEffectsSucceeded')).toEqual([]);
    const calls = rig.boundary.calls.slice(callStart);
    expect(calls[0].method).toBe('register'); expect(calls[0].snapshot?.selectableModelIds).toEqual([]); expect(calls[0].snapshotFrozen).toBe(true);
    expect(calls[0].snapshot?.pendingEffectPlan).toEqual(token);
    expect(calls.map(c => c.method)).toEqual(fault === 'register_invalid' ? ['register'] : ['register', 'set']);
    expect(registryIds(rig)).toEqual(fault === 'register_invalid' ? (direction === 'widening' ? [F] : [A, B, F]) : d.authority.allowedCodexModelIds);
    const selected = rig.session.model;
    if (fault === 'register_invalid') expect(selected).toBe(stale);
    else if (fault === 'reject_after') expect(selected).toBe(requiredModel(rig, after.desiredTarget.selectionModelId!));
    else if (direction === 'widening') {
      const freshSurvivor = requiredModel(rig, F);
      expect(freshSurvivor).not.toBe(stale);
      expect(selected).not.toBe(stale);
      expect(selected).toMatchObject({ provider, id: F });
    } else expect(selected).toBe(stale);
    const staged = models.find(m => m.id === after.desiredTarget.selectionModelId)!;
    await blocked(rig, [stale, staged]);
    expect(rig.boundary.maximumActive).toBeLessThanOrEqual(1);
    expect(after.preRestrictionModelId).toBe(direction === 'widening' ? before.preRestrictionModelId : A);
    expect(after.authorityStatus).toBe(direction === 'legacy' ? 'legacy_restrictive_pending' : 'active');
    if (direction === 'legacy') { expect(after.authoritativeRefreshRequired).toBe(true); expect(rig.http.gets).toBeGreaterThan(1); expect(after.acceptedAuthority).toEqual(before.acceptedAuthority); expect(after.lastLeaseEvidence).toEqual(before.lastLeaseEvidence); expect(after.leaseDeadline).toBe(before.leaseDeadline); }
    else { expect(after.acceptedAuthority).toEqual(d.authority); expect(after.lastLeaseEvidence).toEqual({ evaluatedAt: d.evaluatedAt, validUntil: d.validUntil }); }

    const failedOrdinal = after.lastAllocatedOrdinal;
    const retryStart = rig.boundary.calls.length;
    await rig.controller().retryPiEffects(after.desiredTarget); await rig.controller().whenIdle();
    const retried = rig.controller().snapshot(); expect(retried.lastAllocatedOrdinal).toBe(failedOrdinal + 1n);
    const retryEnvelope = rig.trace.findLast(t => t.kind === 'commit' && t.envelope?.event.type === 'retryPiEffects')!.envelope!;
    expect(retryEnvelope.baseStateRevision).toBe(after.stateRevision); expect(retryEnvelope.allocation.kind).toBe('apply_pi_catalog');
    expect(rig.boundary.calls.slice(retryStart).map(c => c.method)).toEqual(fault === 'reject_after' ? ['register'] : ['register', 'set']);
    if (direction === 'legacy') {
      assertClosed(retried); await blocked(rig, [stale, requiredModel(rig, F)]);
      await rig.respondReadback(d); await rig.controller().whenIdle();
      expect(rig.controller().snapshot().lastAllocatedOrdinal).toBeGreaterThan(retried.lastAllocatedOrdinal);
    }
    expect(rig.controller().snapshot().selectableModelIds).toEqual(d.authority.allowedCodexModelIds);
    expect(rig.controller().snapshot().appliedEffects.target?.targetKind).toBe('active_catalog');
    if (direction === 'widening') { expect(rig.session.model?.id).toBe(A); expect(rig.controller().snapshot().preRestrictionModelId).toBeNull(); }
    const posts = rig.http.posts;
    const allowed = await receiveHeaders(rig, encoded(d)); expect(rig.http.posts).toBe(posts + 1);
    expect((await allowed.release()).stopReason).toBe('stop'); await response.release();
  });

  it.each(['success', 'failure'].flatMap(outcome => ['full', 'legacy'].map(input => ({ outcome, input }))))('real delayed stale $input $outcome cannot acknowledge/fail/cancel superseding authority; single worker repairs', async ({ outcome, input }) => {
    const rig = await productRig({ startup: decision() }); rigs.push(rig);
    await rig.pi.setModel(requiredModel(rig, A)); const old = rig.session.model!;
    rig.boundary.entered = deferred<void>(); rig.boundary.holdSet = deferred<void>();
    if (outcome === 'failure') rig.boundary.fault = 'reject_after';
    const response = await receiveHeaders(rig, input === 'legacy' ? legacy() : encoded(decision('12', true)));
    await rig.boundary.entered.promise;
    const previous = rig.controller().snapshot(); await blocked(rig, [old, requiredModel(rig, F)]);
    const d = decision(input === 'legacy' ? '12' : '13', input === 'legacy'); if (input === 'full') d.authority.allowedCodexModelIds = [B, F];
    const delivered = rig.respondReadback(d);
    if (input === 'full') await rig.controller().requestReadback();
    await delivered;
    const newer = rig.controller().snapshot(); expect(newer.highestSeenGeneration).toBe(d.generation); expect(newer.pendingEffectPlan).not.toEqual(previous.pendingEffectPlan); assertClosed(newer);
    rig.boundary.holdSet.resolve(); rig.boundary.holdSet = null; await rig.controller().whenIdle();
    expect(rig.controller().snapshot().highestSeenGeneration).toBe(d.generation);
    expect(rig.controller().snapshot().selectableModelIds).toEqual(d.authority.allowedCodexModelIds); expect(registryIds(rig)).toEqual(d.authority.allowedCodexModelIds); expect(rig.session.model?.id).toBe(input === 'legacy' ? F : B);
    expect(rig.boundary.maximumActive).toBe(1); await response.release();
  });

  it('false retry after exact expiry cannot reopen or repeat the failed active attempt', async () => {
    const rig = await productRig({ startup: decision() }); rigs.push(rig);
    rig.boundary.fault = 'false'; const response = await receiveHeaders(rig, encoded(decision('12', true))); await rig.controller().whenIdle();
    const target = rig.controller().snapshot().desiredTarget;
    rig.now.value = 1000n * NS; await rig.controller().retryPiEffects(target); await rig.controller().whenIdle();
    assertClosed(rig.controller().snapshot()); expect(rig.controller().snapshot().desiredTarget.targetKind).toBe('closed_catalog');
    await blocked(rig, [models[0] as Parameters<typeof rig.stream>[0], models[2] as Parameters<typeof rig.stream>[0]]); await response.release();
  });
});

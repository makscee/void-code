import { afterAll, afterEach, beforeAll, describe, expect, it, vi } from 'vitest';
import { A, B, F, NS, HttpFixture, assertClosed, assertPins, blocked, bootstrap, configurePinFetch, decision, deferred, delayedHeaders, encoded, headerNames, legacy, models, observedHeaders, productRig, receiveHeaders, registryIds, replaceHeader, requiredModel } from './fixtures/pi-model-decision';
import type { HeaderLine, ProductRig } from './fixtures/pi-model-decision';

beforeAll(async () => { assertPins(); await configurePinFetch(); }, 60_000);
let http: HttpFixture;
beforeAll(() => { http = new HttpFixture(); });
afterAll(async () => { await http?.close(); });

describe('R5 header fixture controls — real HTTP bytes, pinned Fetch only', () => {
  it('unplanned model POST drains to a bounded HTTP trap while a planned POST retains its reply', async () => {
    const before = http.posts;
    const denied = await fetch('http://fixture.invalid/unplanned', { method: 'POST', body: '{}' });
    expect(denied.status).toBe(599); expect(await denied.text()).toBe('fixture-unplanned-model-request'); expect(http.posts).toBe(before + 1);
    http.enqueue({ method: 'POST', headers: [], body: 'planned fixture reply' });
    const planned = await fetch('http://fixture.invalid/planned', { method: 'POST', body: '{}' });
    expect(planned.status).toBe(200); expect(await planned.text()).toBe('planned fixture reply'); expect(http.posts).toBe(before + 2);
  });
  it('HTTP fixture pauses delivery and observes actual consumer read, not only producer emission', async () => {
    const hold = deferred<void>(); const start = http.trace.length;
    http.enqueue({ headers: [], body: 'fixture-body', hold });
    const response = await fetch('http://fixture.invalid/control');
    expect(http.trace.slice(start)).not.toContain('body-emitted');
    const reader = response.body!.getReader(); const read = reader.read();
    expect(http.trace.slice(start)).toContain('body-read-enter'); expect(http.trace.slice(start)).not.toContain('body-byte-consumed');
    hold.resolve(); expect(new TextDecoder().decode((await read).value)).toBe('fixture-body');
    expect(http.trace.slice(start)).toContain('body-byte-consumed'); reader.releaseLock();
  });
  it('two physical fallback fields and one equivalent field are observationally identical; exact single stays distinct', async () => {
    const emittedStart = http.emitted.length;
    const two = await http.headers(replaceHeader(legacy(), 'x-void-quota-state', ['fallback', 'fallback']));
    const single = await http.headers(replaceHeader(legacy(), 'x-void-quota-state', ['fallback, fallback']));
    const valid = await http.headers(legacy());
    expect(http.emitted[emittedStart].match(/x-void-quota-state: fallback\r\n/g)).toHaveLength(2);
    expect(http.emitted[emittedStart + 1].match(/x-void-quota-state:/g)).toHaveLength(1);
    expect(observedHeaders(two.headers)).toEqual(observedHeaders(single.headers));
    expect(two.headers.get('X-VOID-Quota-State')).toBe('fallback, fallback');
    expect(valid.headers.get('x-void-quota-state')).toBe('fallback');
  });

  it.each(headerNames.flatMap(name => [['same', name, 'x', 'x'], ['different', name, 'x', 'y']]))('%s physical values for %s equal one comma-space field', async (_kind, name, a, b) => {
    const separate = await http.headers([[name, a], [name.toUpperCase(), b]]);
    const combined = await http.headers([[name, `${a}, ${b}`]]);
    expect(observedHeaders(separate.headers)).toEqual(observedHeaders(combined.headers));
    expect(separate.headers.get(name)).toBe(`${a}, ${b}`);
    expect(separate.headers.has(name.toUpperCase())).toBe(true);
  });

  it.each(headerNames)('%s absent and present-empty remain distinct', async name => {
    const absent = await http.headers([]);
    const empty = await http.headers([[name, '']]);
    expect([absent.headers.has(name), absent.headers.get(name)]).toEqual([false, null]);
    expect([empty.headers.has(name), empty.headers.get(name)]).toEqual([true, '']);
  });

  it.each([[A, B], [A, A], [B, A], [A, ''], ['', B]])('physical list %s + %s retains inserted OWS, unlike no-OWS list', async (a, b) => {
    const name = 'x-void-allowed-codex-models';
    const two = await http.headers([[name, a], [name, b]]);
    const one = await http.headers([[name, `${a}, ${b}`]]);
    const exact = await http.headers([[name, `${a},${b}`]]);
    expect(two.headers.get(name)).toBe(`${a}, ${b}`);
    expect(one.headers.get(name)).toBe(`${a}, ${b}`);
    expect(exact.headers.get(name)).toBe(`${a},${b}`);
  });

  it('HTTP parser erases leading OWS but retains trailing and embedded OWS', async () => {
    const response = await http.headers([['x-void-quota-state', '\tfallback\t'], ['x-void-effective-model', `${F}\tbad`]]);
    expect(response.headers.get('x-void-quota-state')).toBe('fallback\t');
    expect(response.headers.get('x-void-effective-model')).toBe(`${F}\tbad`);
  });
});

// These witnesses enter the production factory/stream; no fixture closes a gate,
// emits an observeDecision/completion event, or initiates mandatory readback.
const rigs: ProductRig[] = [];
afterEach(async () => { for (const rig of rigs.splice(0).reverse()) await rig.shutdown(); vi.useRealTimers(); });
async function wider(): Promise<ProductRig> {
  const rig = await productRig({ startup: decision() }); rigs.push(rig);
  expect(rig.controller().snapshot().selectableModelIds).toEqual([A, B, F]);
  await rig.pi.setModel(requiredModel(rig, A));
  return rig;
}
function headerCommits(rig: ProductRig) {
  return rig.trace.filter(t => t.kind === 'commit' && t.envelope?.event.type === 'observeDecision' && t.envelope.event.source === 'admission_header');
}

describe('R5 paired production witnesses — actual managed stream Response.headers', () => {
  it.each([
    ['identical physical IDs', [A, A]], ['identical no-OWS IDs', [`${A},${A}`]], ['equivalent coalesced IDs', [`${A}, ${A}`]],
    ['trailing empty physical field', [A, '']], ['leading empty physical field', ['', A]], ['equivalent single empty field', [`${A}, `]],
  ] as const)('%s cannot be deduplicated or filtered into agreeing singleton diagnostics', async (_name, values) => {
    const rig = await wider(); const d = decision('12'); d.authority.allowedCodexModelIds = [A]; d.authority.defaultCodexModelId = d.authority.effectiveCodexModelId = A;
    const response = await receiveHeaders(rig, [...encoded(d), ...values.map(value => ['x-void-allowed-codex-models', value] as const)]); await rig.controller().whenIdle();
    expect(rig.controller().snapshot().authorityStatus).toBe('quarantined'); expect(registryIds(rig)).toEqual([]); assertClosed(rig.controller().snapshot()); await blocked(rig, [models[0]]); await response.release();
  });
  it.each(headerNames.slice(0, 2))('present-empty encoded slot %s is invalid, not the no-headers preservation path', async name => {
    const rig = await wider(); const old = rig.session.model!; const response = await receiveHeaders(rig, [[name, '']]); await rig.controller().whenIdle();
    expect(rig.controller().snapshot().highestSeenGeneration).toBe('11'); assertClosed(rig.controller().snapshot()); expect(registryIds(rig)).toEqual([]); expect(rig.http.gets).toBeGreaterThan(1); await blocked(rig, [old]); await response.release();
  });
  it.each(headerNames.slice(0, 2))('encoded slot %s with observable trailing OWS must not be trimmed into full authority', async name => {
    const rig = await wider(); const lines = [...encoded(decision('12', true)), ...legacy()]; const value = lines.find(([key]) => key === name)![1];
    const response = await receiveHeaders(rig, replaceHeader(lines, name, [`${value}\t`])); await rig.controller().whenIdle();
    expect(rig.controller().snapshot().authorityStatus).toBe('legacy_restrictive_pending'); assertClosed(rig.controller().snapshot()); expect(rig.http.gets).toBeGreaterThan(1); await response.release();
  });
  it.each(['économy', '"economy"', 'economy model'])('header-unsafe but locally compatible wire ID %s cannot prove a legacy singleton', async fallback => {
    const ceiling = models.map(m => m.id === F ? { ...m, id: fallback } : m); const start = decision(); start.authority.allowedCodexModelIds = [A, B, fallback]; start.authority.fallbackCodexModelId = fallback;
    const rig = await productRig({ startup: start, compatibility: ceiling, bootstrapValue: { ...bootstrap, providers: [{ ...bootstrap.providers[0], models: ceiling.map(m => m.id) }] } }); rigs.push(rig);
    const lines = replaceHeader(replaceHeader(legacy(), 'x-void-effective-model', [fallback]), 'x-void-allowed-codex-models', [fallback]);
    const response = await receiveHeaders(rig, lines); await rig.controller().whenIdle(); expect(registryIds(rig)).toEqual([]); assertClosed(rig.controller().snapshot()); expect(rig.controller().snapshot().highestSeenGeneration).toBe('12'); await response.release();
  });
  it('managed stream requires exact void-codex provider even when the model ID is currently allowed', async () => {
    const rig = await wider(); const other = { ...rig.session.model!, provider: 'fixture-unmanaged' }; const posts = rig.http.posts;
    expect(rig.controller().isSelectable(other.provider, other.id)).toBe(false);
    expect((await rig.stream(other)).stopReason).toBe('error'); expect(rig.http.posts).toBe(posts);
  });
  it('configured poll and delayed expiry callback use fixture time; failed readback never renews', async () => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval'] });
    const rig = await wider(); const old = rig.session.model!; const deadline = rig.controller().snapshot().leaseDeadline!;
    await vi.advanceTimersByTimeAsync(16999); expect(rig.http.gets).toBe(1);
    rig.now.value += 17n * NS; await vi.advanceTimersByTimeAsync(1); expect(rig.http.gets).toBeGreaterThan(1);
    rig.http.enqueue({ method: 'GET', status: 503, headers: [], body: 'fixture unavailable' }); await rig.controller().whenIdle();
    expect(rig.controller().snapshot().leaseDeadline).toBe(deadline);
    rig.now.value = deadline + NS; await vi.advanceTimersByTimeAsync(120000); await rig.controller().whenIdle(); assertClosed(rig.controller().snapshot());
    await blocked(rig, [old]); expect(rig.http.posts).toBe(0);
  });
  it('an already-in-flight ordinary poll cannot satisfy post-closure mandatory readback', async () => {
    const rig = await wider(); const pollStarted = rig.http.nextRequest(); const oldPoll = rig.controller().requestReadback(); await pollStarted;
    expect(rig.http.gets).toBe(2);
    const response = await receiveHeaders(rig, legacy());
    expect(rig.http.gets).toBe(3); assertClosed(rig.controller().snapshot());
    await rig.respondReadback(decision()); await oldPoll; await rig.controller().whenIdle();
    expect(rig.controller().snapshot().authorityStatus).toBe('legacy_restrictive_pending'); assertClosed(rig.controller().snapshot());
    await rig.respondReadback(decision('12', true)); await rig.controller().whenIdle();
    expect(rig.controller().snapshot().selectableModelIds).toEqual([F]); await response.release();
  });

  it.each(['quarantined', 'authoritative_empty', 'effect_failed_closed', 'legacy_restrictive_pending'].flatMap(status => ['11', '12', '13'].map(generation => ({ status, generation }))))('in-flight actual headers: $status × $generation cannot lower floor or grant legacy authority', async ({ status, generation }) => {
    const rig = await wider(); const delayed = await delayedHeaders(rig, legacy(generation));
    let second: Awaited<ReturnType<typeof receiveHeaders>> | undefined;
    if (status === 'legacy_restrictive_pending') second = await receiveHeaders(rig, legacy());
    else {
      const d = decision('12', status === 'effect_failed_closed');
      if (status === 'quarantined') d.schemaVersion = 99;
      if (status === 'authoritative_empty') { d.outcome = 'empty_compatible_catalog'; d.authority.allowedCodexModelIds = []; d.authority.defaultCodexModelId = d.authority.effectiveCodexModelId = null; }
      if (status === 'effect_failed_closed') rig.boundary.fault = 'false';
      await rig.readback(d);
    }
    await rig.controller().whenIdle(); const before = rig.controller().snapshot(); await delayed.deliver(); await rig.controller().whenIdle(); const after = rig.controller().snapshot();
    expect(after.highestSeenGeneration).toBe(generation === '13' ? '13' : '12'); assertClosed(after); expect(after.leaseDeadline).toBe(before.leaseDeadline);
    if (generation === '11') { expect(after.desiredTarget).toEqual(before.desiredTarget); expect(after.lastAllocatedOrdinal).toBe(before.lastAllocatedOrdinal); expect(after.preRestrictionModelId).toBe(before.preRestrictionModelId); }
    else if (generation === '13') { expect(after.authorityStatus).toBe('legacy_restrictive_pending'); expect(after.desiredTarget.selectionModelId).toBe(F); }
    else if (status === 'quarantined' || status === 'authoritative_empty') expect(after.authorityStatus).toBe(before.authorityStatus);
    await blocked(rig, [models[0], models[2]]); await delayed.release(); await second?.release();
  });

  it('mandatory readback failure and same-target retry never clear the legacy refresh blocker', async () => {
    const rig = await wider(); const response = await receiveHeaders(rig, legacy()); await rig.controller().whenIdle();
    rig.http.enqueue({ method: 'GET', headers: [], body: 'fixture unavailable', status: 503 }); await rig.controller().whenIdle();
    const before = rig.controller().snapshot(); await rig.controller().retryPiEffects(before.desiredTarget); await rig.controller().whenIdle();
    expect(rig.controller().snapshot().authoritativeRefreshRequired).toBe(true); expect(rig.controller().snapshot().highestSeenGeneration).toBe('12'); expect(rig.controller().snapshot().preRestrictionModelId).toBe(A);
    assertClosed(rig.controller().snapshot()); await blocked(rig, [models[0], models[2]]); await response.release();
  });
  it.each(headerNames.flatMap(name => ['identical', 'different'].flatMap(kind => ['physical', 'combined'].map(shape => ({ name, kind, shape })))))('equivalent $shape $kind pair for $name rejects under production precedence', async ({ name, kind, shape }) => {
    const rig = await wider(); const lines = [...encoded(decision('12', true)), ...legacy()]; const value = lines.find(([key]) => key === name)![1];
    const other = kind === 'identical' ? value : 'different';
    const response = await receiveHeaders(rig, replaceHeader(lines, name, shape === 'physical' ? [value, other] : [`${value}, ${other}`]));
    await rig.controller().whenIdle(); const s = rig.controller().snapshot(); assertClosed(s); expect(rig.http.gets).toBeGreaterThan(1);
    const brokenEncoded = name === headerNames[0] || name === headerNames[1];
    expect(s.desiredTarget.targetKind).toBe(brokenEncoded ? 'legacy_restrictive_catalog' : 'closed_catalog');
    expect(headerCommits(rig).at(-1)?.envelope?.event.bundle).toMatchObject({ [name]: { kind: 'value', combined: `${value}, ${other}` } });
    await blocked(rig, [models[0], models[2]]); await response.release();
  });

  it.each(['unsupported parseable encoded', 'incomplete encoded', 'no fields', 'unrestricted encoded plus fallback', 'empty encoded plus fallback'])('%s cannot create a second permissive authority', async kind => {
    const rig = kind === 'no fields' ? await productRig({ startup: decision('11', true) }) : await wider();
    if (kind === 'no fields') rigs.push(rig);
    let lines: HeaderLine[];
    if (kind === 'unsupported parseable encoded') { const d = decision('13'); d.schemaVersion = 99; lines = [...encoded(d), ...legacy()]; }
    else if (kind === 'incomplete encoded') lines = [encoded(decision('12'))[0], ...legacy()];
    else if (kind === 'unrestricted encoded plus fallback') lines = [...encoded(decision('12')), ...legacy()];
    else if (kind === 'empty encoded plus fallback') { const d = decision('12'); d.outcome = 'empty_compatible_catalog'; d.authority.allowedCodexModelIds = []; d.authority.defaultCodexModelId = d.authority.effectiveCodexModelId = null; lines = [...encoded(d), ...legacy()]; }
    else lines = [];
    const before = rig.controller().snapshot(); const response = await receiveHeaders(rig, lines); await rig.controller().whenIdle();
    const after = rig.controller().snapshot(); expect(rig.http.gets).toBeGreaterThan(1);
    if (kind === 'no fields') { expect(after.leaseDeadline).toBe(before.leaseDeadline); expect(after.desiredTarget).toEqual(before.desiredTarget); }
    else { assertClosed(after); expect(after.highestSeenGeneration).toBe(kind === 'unsupported parseable encoded' ? '13' : '12'); expect(after.desiredTarget.targetKind).toBe(kind === 'incomplete encoded' ? 'legacy_restrictive_catalog' : 'closed_catalog'); }
    expect(headerCommits(rig)).toHaveLength(1); await response.release();
  });
  it.each([['two physical fields', ['fallback', 'fallback']], ['equivalent single field', ['fallback, fallback']]] as const)('%s closes empty before hooks/body/mandatory GET and never advances a split scalar', async (_name, values) => {
    const rig = await wider(); const old = rig.session.model!; const traceStart = rig.http.trace.length;
    const response = await receiveHeaders(rig, replaceHeader(legacy(), 'x-void-quota-state', [...values]));
    await rig.controller().whenIdle();
    const state = rig.controller().snapshot(); expect(state.highestSeenGeneration).toBe('12'); expect(state.authorityStatus).toBe('quarantined'); expect(state.authoritativeRefreshRequired).toBe(true); assertClosed(state);
    expect(registryIds(rig)).toEqual([]); await blocked(rig, [old, models[2] as typeof old]);
    const commits = headerCommits(rig); expect(commits).toHaveLength(1);
    expect(commits[0].envelope?.event.bundle).toMatchObject({ 'x-void-quota-state': { kind: 'value', combined: 'fallback, fallback' } });
    const trace = rig.http.trace.slice(traceStart); const closed = trace.indexOf('publish'); const hook = trace.indexOf('external-response-hook');
    expect(closed).toBeGreaterThanOrEqual(0); expect(closed).toBeLessThan(hook);
    expect(trace.lastIndexOf('GET:/opaque/readback?fixture=1')).toBeGreaterThan(closed);
    expect(rig.http.requests.at(-1)?.wire.toLowerCase()).toContain('cache-control: no-cache');
    expect(rig.http.requests.at(-1)?.wire).toContain('Bearer fixture-not-a-credential');
    await response.release();
    const consumed = rig.http.trace.slice(traceStart);
    expect(consumed.indexOf('body-read-enter')).toBeGreaterThan(consumed.indexOf('publish'));
    expect(consumed.indexOf('body-byte-consumed')).toBeGreaterThan(consumed.indexOf('publish'));
  });

  it('exact seven-field fallback immediately stages singleton, stays closed after Pi success, then requires fresh full-authority token', async () => {
    const rig = await wider(); const old = rig.session.model!; const before = rig.controller().snapshot(); const traceStart = rig.http.trace.length;
    rig.workerBarrier.value = deferred<void>();
    const response = await receiveHeaders(rig, legacy());
    const pending = rig.controller().snapshot(); assertClosed(pending);
    expect(pending.highestSeenGeneration).toBe('12'); expect(pending.authorityStatus).toBe('legacy_restrictive_pending'); expect(pending.preRestrictionModelId).toBe(A);
    expect(pending.leaseDeadline).toBe(before.leaseDeadline); expect(pending.authoritativeRefreshRequired).toBe(true);
    expect(pending.pendingEffectPlan?.target).toMatchObject({ targetKind: 'legacy_restrictive_catalog', safetyGeneration: '12', inputFingerprint: null, authorityDigest: null, selectionModelId: F });
    expect(pending.pendingEffectPlan?.target.legacyProjectionDigest).toMatch(/^v1:[0-9a-f]{64}$/);
    const commit = headerCommits(rig).at(-1)!; expect(commit.envelope?.allocation).toEqual({ kind: 'apply_pi_catalog', token: pending.pendingEffectPlan });
    expect(commit.envelope?.baseStateRevision).toBe(before.stateRevision); expect(commit.envelope?.branchContextEpoch).toBe(before.branchContextEpoch);
    const bundle = commit.envelope?.event.bundle as Record<string, unknown>;
    expect(Object.keys(bundle).sort()).toEqual([...headerNames].sort()); expect(commit.bundleFrozen).toBe(true);
    expect(bundle['x-void-model-decision']).toEqual({ kind: 'absent' });
    const trace = rig.http.trace.slice(traceStart);
    expect(trace.indexOf('enqueue-effects')).toBeGreaterThan(trace.indexOf('publish'));
    expect(trace.indexOf('external-response-hook')).toBeGreaterThan(trace.indexOf('enqueue-effects'));
    expect(trace.indexOf('GET:/opaque/readback?fixture=1')).toBeGreaterThan(trace.indexOf('enqueue-effects'));
    expect(rig.trace.findLast(t => t.kind === 'readback-request')).toMatchObject({ cache: 'no-store', url: bootstrap.modelDecision.readbackUrl, controllerInstanceId: pending.controllerInstanceId, branchContextEpoch: pending.branchContextEpoch });
    expect(trace).not.toContain('register-entry'); expect(trace).not.toContain('body-emitted');
    await blocked(rig, [old]);
    rig.workerBarrier.value.resolve(); rig.workerBarrier.value = null; await rig.controller().whenIdle();
    expect(registryIds(rig)).toEqual([F]); expect(rig.session.model?.id).toBe(F); assertClosed(rig.controller().snapshot()); await blocked(rig, [old, requiredModel(rig, F)]);
    const legacyOrdinal = rig.controller().snapshot().lastAllocatedOrdinal;
    await rig.respondReadback(decision('12', true));
    await rig.controller().whenIdle();
    expect(rig.controller().snapshot().lastAllocatedOrdinal).toBeGreaterThan(legacyOrdinal);
    expect(rig.controller().snapshot().appliedEffects.target?.targetKind).toBe('active_catalog'); expect(rig.controller().snapshot().selectableModelIds).toEqual([F]);
    expect(rig.boundary.calls.filter(c => c.method === 'register').slice(-2).map(c => c.ids)).toEqual([[F], [F]]);
    expect((await response.release()).stopReason).toBe('stop'); // Already-dispatched request is not retroactively revoked.
    const consumed = rig.http.trace.slice(traceStart);
    expect(consumed.indexOf('register-entry')).toBeGreaterThan(consumed.indexOf('publish'));
    expect(consumed.indexOf('body-read-enter')).toBeGreaterThan(consumed.indexOf('publish'));
    expect(consumed.indexOf('body-byte-consumed')).toBeGreaterThan(consumed.indexOf('publish'));
  });

  const malformed: [string, HeaderLine[]][] = [
    ...legacy().flatMap(([name, value]): [string, HeaderLine[]][] => [
      [`missing ${name}`, replaceHeader(legacy(), name, [])], [`empty ${name}`, replaceHeader(legacy(), name, [''])],
      [`duplicate ${name}`, replaceHeader(legacy(), name, [value, value])], [`combined ${name}`, replaceHeader(legacy(), name, [`${value}, ${value}`])],
      [`different duplicate ${name}`, replaceHeader(legacy(), name, [value, 'different'])], [`different combined ${name}`, replaceHeader(legacy(), name, [`${value}, different`])],
      [`comma no OWS ${name}`, replaceHeader(legacy(), name, [`${value},${value}`])], [`observable OWS ${name}`, replaceHeader(legacy(), name, [`${value}\tbad`])], [`trailing OWS ${name}`, replaceHeader(legacy(), name, [`${value}\t`])],
    ]),
    ...['x-void-model-decision-generation', 'x-void-quota-policy-revision', 'x-void-quota-episode'].flatMap(name => ['0', '+1', '01', '9223372036854775808'].map(value => [`${name}=${value}`, replaceHeader(legacy(), name, [value])] as [string, HeaderLine[]])),
    ['wrong singleton', replaceHeader(legacy(), 'x-void-allowed-codex-models', [A])],
    ['multi-model singleton', replaceHeader(legacy(), 'x-void-allowed-codex-models', [`${F},${A}`])],
    ['quoted', replaceHeader(legacy(), 'x-void-effective-model', [`"${F}"`])],
    ['non-ASCII', replaceHeader(legacy(), 'x-void-effective-model', ['é'])],
    ['model bound', replaceHeader(legacy(), 'x-void-effective-model', ['a'.repeat(129)])],
    ['snapshot bound', replaceHeader(legacy(), 'x-void-quota-state', ['x'.repeat(21849)])],
    ['case-sensitive value', replaceHeader(legacy(), 'x-void-quota-state', ['Fallback'])],
  ];
  it.each(malformed)('grammar %s: empty target/no model POST; never repair or splice', async (_name, lines) => {
    const rig = await wider(); const old = rig.session.model!; const before = rig.controller().snapshot();
    const response = await receiveHeaders(rig, lines); await rig.controller().whenIdle();
    assertClosed(rig.controller().snapshot()); expect(registryIds(rig)).toEqual([]); await blocked(rig, [old]);
    const generation = lines.filter(([name]) => name === 'x-void-model-decision-generation').map(([, value]) => value).join(', ');
    expect(rig.controller().snapshot().highestSeenGeneration).toBe(generation === '12' ? '12' : before.highestSeenGeneration);
    expect(rig.http.gets).toBeGreaterThan(1); await response.release();
  });

  it.each(['absent', 'duplicate'])('compatible definition %s cannot substitute local/default model', async kind => {
    const compatibility = kind === 'absent' ? models.filter(m => m.id !== F) : [...models, models[2]];
    const start = decision(); start.authority.allowedCodexModelIds = [A, B];
    const rig = await productRig({ startup: start, compatibility }); rigs.push(rig);
    const old = rig.session.model!; const response = await receiveHeaders(rig, legacy()); await rig.controller().whenIdle();
    expect(registryIds(rig)).toEqual([]); expect(rig.controller().snapshot().desiredTarget.selectionModelId).toBeNull(); assertClosed(rig.controller().snapshot());
    await blocked(rig, [old, models[2]]); expect(rig.http.gets).toBeGreaterThan(1); await response.release();
  });

  it.each(['normal', 'warning'])('individual %s cannot broaden or renew existing restricted authority', async quota => {
    const rig = await productRig({ startup: decision('11', true) }); rigs.push(rig);
    const before = rig.controller().snapshot();
    const lines = replaceHeader(replaceHeader(legacy('12'), 'x-void-quota-state', [quota]), 'x-void-model-selection-restricted', ['false']);
    const response = await receiveHeaders(rig, lines);
    const after = rig.controller().snapshot(); expect(after.acceptedAuthority).toEqual(before.acceptedAuthority); expect(after.leaseDeadline).toBe(before.leaseDeadline); expect(after.selectableModelIds).toEqual([F]);
    expect(rig.http.gets).toBeGreaterThan(1); await response.release();
  });

  it.each([['two physical allowed fields', [A, B]], ['equivalent single allowed field', [`${A}, ${B}`]], ['identical IDs', [`${A},${A}`]], ['reversed order', [`${B},${A}`]], ['embedded whitespace', [`${A},\t${B}`]]] as const)('%s beside encoded authority quarantines', async (_name, values) => {
    const rig = await wider(); const d = decision('12'); d.authority.allowedCodexModelIds = [A, B];
    const response = await receiveHeaders(rig, [...encoded(d), ...values.map(v => ['x-void-allowed-codex-models', v] as const)]);
    expect(rig.controller().snapshot().authorityStatus).toBe('quarantined'); assertClosed(rig.controller().snapshot()); await response.release();
  });

  it.each(['none', 'partial', 'exact ordered list', 'restricted seven'])('encoded agreement with %s diagnostics commits once without redundant legacy readback', async kind => {
    const rig = await wider(); const d = decision('12', kind === 'restricted seven');
    const diagnostics: HeaderLine[] = kind === 'restricted seven' ? legacy() : kind === 'partial' ? [['x-void-quota-state', 'normal']] : kind === 'exact ordered list' ? [['x-void-allowed-codex-models', `${A},${B},${F}`]] : [];
    const gets = rig.http.gets;
    const response = await receiveHeaders(rig, [...encoded(d), ...diagnostics]); await rig.controller().whenIdle();
    expect(headerCommits(rig)).toHaveLength(1); expect(rig.controller().snapshot().acceptedAuthority).toEqual(d.authority); expect(rig.http.gets).toBe(gets);
    expect(rig.controller().snapshot().selectableModelIds).toEqual(d.authority.allowedCodexModelIds); await response.release();
    expect(rig.http.gets).toBeGreaterThan(gets);
  });

  it.each(headerNames)('encoded precedence: malformed present %s never overrides complete authority', async name => {
    const rig = await wider(); const lines = replaceHeader([...encoded(decision('12', true)), ...legacy()], name, ['broken, broken']);
    const response = await receiveHeaders(rig, lines); await rig.controller().whenIdle();
    assertClosed(rig.controller().snapshot()); expect(rig.http.gets).toBeGreaterThan(1);
    expect(rig.controller().snapshot().desiredTarget.targetKind).toBe(name.startsWith('x-void-model-decision') && name !== 'x-void-model-decision-generation' ? 'legacy_restrictive_catalog' : 'closed_catalog');
    await response.release();
  });

  it('fallback with unambiguous lower generation is stale even when malformed', async () => {
    const rig = await wider(); const before = rig.controller().snapshot();
    const response = await receiveHeaders(rig, replaceHeader(legacy('10'), 'x-void-quota-state', ['fallback', 'fallback']));
    const after = rig.controller().snapshot(); expect(after.desiredTarget).toEqual(before.desiredTarget); expect(after.leaseDeadline).toBe(before.leaseDeadline); expect(after.pendingEffectPlan).toEqual(before.pendingEffectPlan); expect(after.preRestrictionModelId).toBe(before.preRestrictionModelId);
    await response.release();
  });

  it('stream reads current monotonic deadline even with a delayed expiry callback', async () => {
    const rig = await wider(); const old = rig.session.model!;
    rig.now.value = rig.controller().snapshot().leaseDeadline!;
    await blocked(rig, [old]); expect(rig.http.posts).toBe(0);
  });

  it('header snapshots from separate responses are never merged', async () => {
    const rig = await wider(); const response = await receiveHeaders(rig, legacy().slice(0, 3));
    assertClosed(rig.controller().snapshot()); expect(registryIds(rig)).not.toContain(A);
    await response.release(); await blocked(rig, [models[0] as Parameters<typeof rig.stream>[0]]);
    expect(headerCommits(rig)).toHaveLength(1);
  });

  it('success dequeue pause cannot authorize a staged model; expiry wins over queued success', async () => {
    const rig = await wider(); rig.dequeueBarrier.value = deferred<void>();
    const response = await receiveHeaders(rig, encoded(decision('12', true)));
    await rig.boundary.entered.promise;
    const staged = requiredModel(rig, F);
    await blocked(rig, [models[0] as Parameters<typeof rig.stream>[0], staged]);
    rig.now.value = 1000n * NS;
    rig.dequeueBarrier.value.resolve(); rig.dequeueBarrier.value = null; await rig.controller().whenIdle();
    assertClosed(rig.controller().snapshot());
    expect(registryIds(rig)).toEqual([]); expect(rig.registry.find(staged.provider, F)).toBeUndefined();
    const posts = rig.http.posts; await blocked(rig, [staged]); expect(rig.http.posts).toBe(posts);
    await response.release();
  });
});

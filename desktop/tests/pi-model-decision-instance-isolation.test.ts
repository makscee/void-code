import { afterEach, beforeAll, describe, expect, it } from 'vitest';
import {
  bootstrap,
  canonical,
  completedSSE,
  configurePinFetch,
  decision,
  deferred,
  HttpFixture,
  managed,
  models,
  NS,
  pinRig,
  provider,
  assertPins,
} from './fixtures/pi-model-decision';

type Rig = Awaited<ReturnType<typeof pinRig>>;
type Product = Awaited<ReturnType<typeof managed>>;

type Transport = typeof bootstrap;

const activeRigs: Rig[] = [];
const activeHttp: HttpFixture[] = [];

afterEach(async () => {
  const rigs = activeRigs.splice(0).reverse();
  const httpFixtures = activeHttp.splice(0).reverse();
  try {
    for (const rig of rigs) {
      try { await rig.session.extensionRunner.emit({ type: 'session_shutdown', reason: 'quit' }); }
      finally { rig.close(); }
    }
  } finally {
    for (const http of httpFixtures) await http.close();
  }
});

beforeAll(async () => {
  assertPins();
  await configurePinFetch();
}, 60_000);

function transport(label: string): Transport {
  return {
    ...structuredClone(bootstrap),
    relayUrl: `http://fixture.invalid/relay-${label}`,
    authToken: `opaque-bearer-${label}`,
    providers: [{ ...bootstrap.providers[0], relayProviderId: `opaque-provider-${label}` }],
    modelDecision: {
      ...bootstrap.modelDecision,
      readbackUrl: `http://fixture.invalid/readback-${label}`,
    },
  };
}

async function openInstance(product: Product, http: HttpFixture, value: Transport): Promise<Rig> {
  const readbackStarted = http.nextRequest();
  const applied = deferred<void>();
  const rig = await pinRig(async pi => {
    await product.default(pi, {
      modelDecision: {
        bootstrap: structuredClone(value),
        compatibilityModels: models,
        nowMonoNs: () => 12n * NS,
        observe: (record: Record<string, unknown>) => {
          const envelope = record.envelope as { event?: { source?: unknown } } | undefined;
          if (record.kind === 'commit' && envelope?.event?.source === 'readback') applied.resolve();
        },
      },
    });
  });
  activeRigs.push(rig);
  await readbackStarted;
  http.enqueue({
    method: 'GET',
    urlPath: new URL(value.modelDecision.readbackUrl).pathname,
    headers: [['content-type', 'application/json']],
    body: canonical(decision('11')),
  });
  await applied.promise;
  const controller = product.getModelDecisionController(rig.pi);
  await controller.whenIdle();
  expect(controller.snapshot().authorityStatus).toBe('active');
  expect(controller.snapshot().appliedEffects.status).toBe('applied');
  return rig;
}

describe.sequential('managed stream authority is isolated per Pi instance', () => {
  it('keeps A transport and permission after the same module initializes B', async () => {
    const product = await managed();
    const http = new HttpFixture();
    activeHttp.push(http);
    const aValue = transport('a');
    const bValue = transport('b');
    const a = await openInstance(product, http, aValue);
    const aController = product.getModelDecisionController(a.pi);
    const aRegistration = a.runtime.getRegisteredProviderConfig(provider);
    expect(aRegistration?.streamSimple).toBeDefined();
    expect(aRegistration).toMatchObject({
      baseUrl: aValue.relayUrl,
      apiKey: aValue.authToken,
      headers: { 'x-void-provider': aValue.providers[0].relayProviderId },
    });

    const bStarted = http.nextRequest();
    const b = await pinRig(async pi => {
      await product.default(pi, {
        modelDecision: {
          bootstrap: structuredClone(bValue),
          compatibilityModels: models,
          nowMonoNs: () => 12n * NS,
        },
      });
    });
    activeRigs.push(b);
    await bStarted;
    http.enqueue({ method: 'GET', urlPath: new URL(bValue.modelDecision.readbackUrl).pathname, status: 503, headers: [], body: 'fixture-b-closed' });
    const bController = product.getModelDecisionController(b.pi);
    expect(bController.isSelectable(provider, models[0].id)).toBe(false);
    expect(aController.isSelectable(provider, models[0].id)).toBe(true);

    http.enqueue({
      method: 'POST',
      urlPath: `${new URL(aValue.relayUrl).pathname}/codex/responses`,
      headers: [['content-type', 'text/event-stream']],
      body: completedSSE,
    });
    const events = aRegistration!.streamSimple!(
      { ...models[0], baseUrl: aValue.relayUrl, provider },
      { messages: [], systemPrompt: 'fixture' },
    );
    for await (const event of events) { void event; }
    const result = await events.result();
    const request = http.requests.find(item => item.method === 'POST' && item.path === `${new URL(aValue.relayUrl).pathname}/codex/responses`);
    expect(result.stopReason, result.errorMessage).toBe('stop');
    expect(request?.wire).toContain(`authorization: Bearer ${aValue.authToken}`);
    expect(request?.wire).toContain(`x-void-provider: ${aValue.providers[0].relayProviderId}`);
    expect(request?.wire).not.toContain(bValue.authToken);
    expect(request?.wire).not.toContain(bValue.providers[0].relayProviderId);
    expect(aController.isSelectable(provider, models[0].id)).toBe(true);
    expect(bController.isSelectable(provider, models[0].id)).toBe(false);
  });
});

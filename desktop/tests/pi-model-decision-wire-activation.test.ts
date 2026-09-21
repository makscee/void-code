import { chmodSync, mkdtempSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, expect, it } from 'vitest';
import { bootstrap, managed, pinRig, registryIds } from './fixtures/pi-model-decision';

let activeRig: Awaited<ReturnType<typeof pinRig>> | undefined;
afterEach(() => {
  activeRig?.close();
  activeRig = undefined;
});

it.each([
  ['not-a-url', false],
  ['/relative', false],
  ['ftp://relay.fixture.invalid', false],
  ['https://user:pass@relay.fixture.invalid', false],
  ['https://relay.fixture.invalid:443', true],
] as const)('validates the V2 relay bootstrap URL %s', async (relayUrl, expected) => {
  const product = await managed();
  const result = product.parseBootstrap({ ...structuredClone(bootstrap), relayUrl });
  expect(result.ok).toBe(expected);
  if (!expected) expect(result).toEqual({ ok: false });
});

it('fails closed when the live loader receives V1 instead of registering a managed catalog', async () => {
  const work = mkdtempSync(path.join(os.tmpdir(), 'void-model-wire-'));
  const bootstrap = JSON.stringify({
    version: 1,
    relayUrl: 'http://fixture.invalid',
    authToken: 'fixture-not-a-credential',
    providers: [{ kind: 'codex', relayProviderId: 'opaque-fixture-route', models: ['gpt-5.6-terra'] }],
  });
  const executable = path.join(work, 'vc-bootstrap');
  writeFileSync(executable, `#!/bin/sh\n[ "$1" = "pi-bootstrap" ] || exit 1\nprintf '%s' '${bootstrap}'\n`, { mode: 0o700 });
  chmodSync(executable, 0o700);

  const previous = process.env.VC_BOOTSTRAP_EXECUTABLE;
  process.env.VC_BOOTSTRAP_EXECUTABLE = executable;
  try {
    const product = await managed();
    activeRig = await pinRig(async pi => { await product.default(pi, undefined); });

    expect(activeRig.runtime.getRegisteredProviderConfig('void-codex')).toBeUndefined();
    expect(registryIds(activeRig)).toEqual([]);
  } finally {
    if (previous === undefined) delete process.env.VC_BOOTSTRAP_EXECUTABLE;
    else process.env.VC_BOOTSTRAP_EXECUTABLE = previous;
  }
});

// Fake-only control for the frozen acceptance stderr repair. No Pi imports or clipboard IO.
import { mkdtempSync, rmSync, writeFileSync, readFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { createRequire } from 'node:module';
import { runInNewContext } from 'node:vm';
import { transformSync } from 'esbuild';
import { expect, it } from 'vitest';

const require = createRequire(import.meta.url);
const acceptance = transformSync(readFileSync(path.resolve('tests/pi-fullscreen-native-acceptance.test.ts'), 'utf8'), {
  loader: 'ts', format: 'cjs', target: 'node22',
}).code;

it.each([0, 7])('fake Node exit %i retains stderr when native marker is missing', (status) => {
  const work = mkdtempSync(path.join(tmpdir(), 'astra-native-diagnostic-'));
  try {
    // Explicit Node entry ignores all Pi arguments; the copied probe is NEVER loaded.
    const entry = path.join(work, 'fake-node-entry.cjs');
    writeFileSync(entry, `process.stderr.write('FAKE_NATIVE_FAILURE_ONLY\\n'); process.stdout.write('fake model table\\n'); process.exitCode = ${status};`);
    let acceptanceBody: (() => void) | undefined;
    runInNewContext(acceptance, {
      // Capture one test body, not a nested Vitest process/test suite.
      require: (id: string) => {
        if (id === 'vitest') return { expect, it: { skipIf: (skip: boolean) => {
          expect(skip).toBe(false);
          return (_name: string, body: () => void) => { acceptanceBody = body; };
        } } };
        if (id === './fixtures/pi-fullscreen-clipboard') return { embeddedSource: () => '// fake-only; never imported' };
        if (id.startsWith('node:')) return require(id);
        throw new Error('Unexpected acceptance dependency');
      },
      process: {
        execPath: process.execPath, platform: 'win32',
        env: {
          PATH: process.env.PATH, SystemRoot: process.env.SystemRoot, WINDIR: process.env.WINDIR,
          VC_NATIVE_PI_ENTRY: entry, VC_NATIVE_PI_PACKAGE_DIR: work,
          VC_ISOLATED_CLIPBOARD_ACCEPTANCE: 'I_OWN_THIS_ISOLATED_CLIPBOARD_SESSION',
        },
      },
    });
    expect(acceptanceBody).toBeTypeOf('function');
    let failure: unknown;
    try { acceptanceBody!(); } catch (error) { failure = error; }
    expect(failure).toBeDefined();
    const message = String(failure);
    expect(message).toContain(`Pi status=${status}`);
    expect(message).toContain('stderr:\nFAKE_NATIVE_FAILURE_ONLY');
    expect(message).toContain('stdout:\nfake model table');
    if (status === 0) expect(message).toContain('Required ASTRA_NATIVE_SELECTION_READBACK_OK_4');
  } finally { rmSync(work, { recursive: true, force: true }); }
});

import * as actualFs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it, vi } from 'vitest';

vi.mock('node:fs', async (importOriginal) => {
  const fs = await importOriginal<typeof actualFs>();
  const readOnlyTemporaryHandles = new Set<number>();
  return {
    ...fs,
    openSync(file: actualFs.PathLike, flags: actualFs.OpenMode, mode?: actualFs.Mode) {
      const handle = fs.openSync(file, flags, mode);
      if (String(file).endsWith('.tmp') && flags === 'r') readOnlyTemporaryHandles.add(handle);
      return handle;
    },
    fsyncSync(handle: number) {
      if (readOnlyTemporaryHandles.has(handle)) {
        const error = new Error('EPERM: operation not permitted, fsync');
        Object.assign(error, { code: 'EPERM' });
        throw error;
      }
      fs.fsyncSync(handle);
    },
    closeSync(handle: number) {
      readOnlyTemporaryHandles.delete(handle);
      fs.closeSync(handle);
    },
  };
});

import { startupDiagnostic, writeStartupDiagnostic } from '../src/main/startup-diagnostic';

const roots: string[] = [];
afterEach(() => { for (const root of roots.splice(0)) actualFs.rmSync(root, { recursive: true, force: true }); });

describe('Windows startup diagnostic durability', () => {
  it('flushes the temporary diagnostic through a writable handle', () => {
    const root = path.join(os.tmpdir(), `void-startup-windows-${crypto.randomUUID()}`);
    roots.push(root);
    actualFs.mkdirSync(root);

    const file = writeStartupDiagnostic(root, startupDiagnostic('renderer-load', new Error('renderer failed to load'), '0.1.0'));

    expect(JSON.parse(actualFs.readFileSync(file, 'utf8'))).toMatchObject({ code: 'STARTUP_FAILED' });
  });
});

import { chmodSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import { resolvePrivateRuntime, sha256File, treeSha256 } from '../src/main/resources';

const roots: string[] = [];
function fixture(): string {
  const root = path.join(os.tmpdir(), `private-runtime-${crypto.randomUUID()}`); roots.push(root);
  mkdirSync(path.join(root, 'vc/bin'), { recursive: true }); mkdirSync(path.join(root, 'node/bin'), { recursive: true }); mkdirSync(path.join(root, 'pi'), { recursive: true }); mkdirSync(path.join(root, 'fixture'), { recursive: true });
  for (const file of ['vc/bin/vc', 'node/bin/node', 'pi/cli.js', 'fixture/test.js']) { writeFileSync(path.join(root, file), file); chmodSync(path.join(root, file), 0o755); }
  const manifest = { schema: 1, platform: 'darwin-arm64', vc: { version: 'v', sourceCommit: 'c', path: 'vc/bin/vc', sha256: sha256File(path.join(root, 'vc/bin/vc')) }, node: { version: 'v', path: 'node/bin/node', sha256: sha256File(path.join(root, 'node/bin/node')) }, pi: { version: 'v', entry: 'pi/cli.js', treeSha256: treeSha256(path.join(root, 'pi')) }, fixture: { path: 'fixture/test.js', sha256: sha256File(path.join(root, 'fixture/test.js')) } };
  writeFileSync(path.join(root, 'manifest.json'), JSON.stringify(manifest)); return root;
}
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

describe('private runtime manifest', () => {
  it('resolves only manifest-owned files without PATH lookup', () => { const root = fixture(); const runtime = resolvePrivateRuntime(root); expect(runtime.node).toBe(path.join(root, 'node/bin/node')); });
  it('rejects changed assets and path escape', () => { const root = fixture(); writeFileSync(path.join(root, 'node/bin/node'), 'changed'); expect(() => resolvePrivateRuntime(root)).toThrow('hash mismatch'); });
  it('rejects resources inside asar', () => { expect(() => resolvePrivateRuntime('/tmp/app.asar/private-runtime')).toThrow('outside asar'); });
  it('produces a stable tree hash independent of file creation order', () => { const first = fixture(); const second = fixture(); expect(treeSha256(path.join(first, 'pi'))).toBe(treeSha256(path.join(second, 'pi'))); expect(readFileSync(path.join(first, 'pi/cli.js'), 'utf8')).toBe('pi/cli.js'); });
});

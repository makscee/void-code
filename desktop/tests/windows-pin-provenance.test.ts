import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import pins from '../scripts/resource-pins.json';

const assembly = readFileSync(path.resolve('scripts/assemble-windows-resources.mjs'), 'utf8');
const builder = readFileSync(path.resolve('scripts/windows-vc-build.mjs'), 'utf8');
const workflow = readFileSync(path.resolve('../.github/workflows/desktop-windows-app.yml'), 'utf8');

/**
 * Windows must package vc built from the checkout that triggered this run. A
 * public-release pin can only point backwards before the next tag exists, so it
 * is intrinsically incapable of carrying the source being released.
 */
describe('windows vc checkout provenance', () => {
  it('does not retain a static release pin for vc', () => {
    expect((pins.windows as Record<string, unknown>).vc).toBeUndefined();
    expect(workflow).not.toMatch(/releases\/download|runtime\/cache\/vc/);
  });

  it('builds the exact windows amd64 CLI from this checkout', () => {
    expect(builder).toMatch(/execFileSync\('go', \[\s*'build'/);
    expect(builder).toContain("GOOS: 'windows'");
    expect(builder).toContain("GOARCH: 'amd64'");
    expect(builder).toContain("'./cmd/vc'");
    expect(builder).toContain('gitHead(repo)');
  });

  it('requires workflow-bound source and version identities', () => {
    expect(workflow).toContain('VOID_DESKTOP_VC_SOURCE_COMMIT: ${{ github.sha }}');
    expect(workflow).toContain('VOID_DESKTOP_VC_VERSION: ${{ github.ref_name }}');
    expect(assembly).toContain('VOID_DESKTOP_VC_SOURCE_COMMIT');
    expect(assembly).toContain('VOID_DESKTOP_VC_VERSION');
    expect(builder).toContain('internal/version.Version=');
  });

  it('records and verifies the built asset identity', () => {
    expect(builder).toContain("assetName: 'vc-windows-amd64.exe'");
    expect(builder).toContain('sourceCommit: checkoutCommit');
    expect(builder).toContain('version: builtVersion');
    expect(builder).toContain('builtVersion !== expectedOutput');
    expect(builder).toContain('sha256: await shaFile(destination)');
    expect(assembly).toContain('sourceCommit: expectedSourceCommit');
    expect(assembly).toContain('version: expectedVersion');
  });
});

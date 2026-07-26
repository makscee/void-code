import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import {
  assertCandidateManifest, assertRepositoryReady, buildCandidateManifest, CANONICAL_ORIGIN, serializeCandidateManifest, verifyCandidateArtifacts,
} from '../scripts/candidate-manifest-lib.mjs';

const roots: string[] = [];
const sha = (text: string) => createHash('sha256').update(text).digest('hex');
function temporary(prefix: string) { const root = mkdtempSync(path.join(os.tmpdir(), prefix)); roots.push(root); return root; }
function fixture() {
  const root = temporary('vc-candidate-');
  const installer = path.join(root, 'Void-Code-0.1.0-windows-x64.exe');
  const resources = path.join(root, 'manifest.json');
  writeFileSync(installer, 'exact unsigned installer fixture');
  writeFileSync(resources, JSON.stringify({ schema: 1, platform: 'win32-x64', vc: {}, node: {}, pi: {} }));
  return { root, installer, resources };
}
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

describe('candidate manifest provenance', () => {
  it('builds and validates one deterministic unsigned manifest from exact files', () => {
    const { installer, resources } = fixture();
    const input = {
      productName: 'Void Code', version: '0.1.0', sourceCommit: '1'.repeat(40), sourceOrigin: CANONICAL_ORIGIN, arch: 'x64',
      installerPath: installer, resourceManifestPath: resources, buildTimestamp: '2026-07-27T12:34:56.000Z',
      predecessorReference: 'VC14-prototype-80487da8', predecessorSha256: 'a'.repeat(64),
      operatorGate: 'blocked', gateEvidence: 'VC-19', gateVerifiedAt: null,
    };
    const first = serializeCandidateManifest(buildCandidateManifest(input));
    const second = serializeCandidateManifest(buildCandidateManifest(input));
    expect(second).toBe(first);
    const manifest = assertCandidateManifest(JSON.parse(first));
    expect(manifest).toMatchObject({
      schema: 1, product: { name: 'Void Code', version: '0.1.0' }, source: { commit: '1'.repeat(40), branch: 'main', remote: 'origin/main', originUrl: CANONICAL_ORIGIN },
      installer: { basename: 'Void-Code-0.1.0-windows-x64.exe', sha256: sha('exact unsigned installer fixture'), arch: 'x64' },
      predecessor: { reference: 'VC14-prototype-80487da8', installerSha256: 'a'.repeat(64) }, signing: { status: 'unsigned' },
      operatorGate: { status: 'blocked', evidence: 'VC-19', verifiedAt: null },
    });
  });

  it('rejects dirty/diverged source and malformed or mutable inputs', () => {
    const clean = { branch: 'main', upstream: 'origin/main', originUrl: CANONICAL_ORIGIN, status: '', head: '1'.repeat(40), upstreamHead: '1'.repeat(40), remoteHead: '1'.repeat(40) };
    expect(assertRepositoryReady(clean)).toBe('1'.repeat(40));
    for (const changed of [
      { status: '?? secret.txt' }, { branch: 'pilot' }, { upstream: 'fork/main' }, { originUrl: 'https://github.com/attacker/fork.git' }, { upstreamHead: '2'.repeat(40) }, { remoteHead: '3'.repeat(40) },
    ]) expect(() => assertRepositoryReady({ ...clean, ...changed })).toThrow();
    const { installer, resources } = fixture();
    const base = { productName: 'Void Code', version: '0.1.0', sourceCommit: '1'.repeat(40), sourceOrigin: CANONICAL_ORIGIN, arch: 'x64', installerPath: installer, resourceManifestPath: resources, buildTimestamp: '2026-07-27T12:34:56.000Z', predecessorReference: 'stable-previous', predecessorSha256: 'a'.repeat(64), operatorGate: 'blocked', gateEvidence: 'VC-19', gateVerifiedAt: null };
    for (const changed of [
      { predecessorReference: 'latest' }, { predecessorSha256: 'abc' }, { buildTimestamp: 'now' }, { operatorGate: 'verified' }, { gateEvidence: 'pending' },
    ]) expect(() => buildCandidateManifest({ ...base, ...changed })).toThrow();
    expect(() => assertCandidateManifest({ ...buildCandidateManifest(base), token: 'forbidden' })).toThrow('unknown or missing');
  });

  it('checks exact artifact bytes and the CLI rejects a clean synchronized fork', () => {
    const artifacts = fixture();
    const manifest = buildCandidateManifest({ productName: 'Void Code', version: '0.1.0', sourceCommit: '1'.repeat(40), sourceOrigin: CANONICAL_ORIGIN, arch: 'x64', installerPath: artifacts.installer, resourceManifestPath: artifacts.resources, buildTimestamp: '2026-07-27T12:34:56.000Z', predecessorReference: 'VC14-prototype-80487da8', predecessorSha256: 'a'.repeat(64), operatorGate: 'blocked', gateEvidence: 'VC-19', gateVerifiedAt: null });
    expect(verifyCandidateArtifacts(manifest, artifacts.installer, artifacts.resources)).toBe(manifest);
    writeFileSync(artifacts.installer, 'changed installer');
    expect(() => verifyCandidateArtifacts(manifest, artifacts.installer, artifacts.resources)).toThrow('hash or size mismatch');

    const repo = temporary('vc-candidate-fork-'); const remote = temporary('vc-candidate-fork-remote-');
    execFileSync('git', ['init', '--bare', remote]); execFileSync('git', ['init', '-b', 'main', repo]);
    execFileSync('git', ['-C', repo, 'config', 'user.name', 'Fixture']); execFileSync('git', ['-C', repo, 'config', 'user.email', 'fixture@example.invalid']);
    mkdirSync(path.join(repo, 'desktop')); writeFileSync(path.join(repo, 'desktop/package.json'), JSON.stringify({ version: '0.1.0', build: { productName: 'Void Code', appId: 'works.voidcode.desktop', nsis: { artifactName: 'Void-Code-${version}-windows-${arch}.${ext}' } } }));
    execFileSync('git', ['-C', repo, 'add', '.']); execFileSync('git', ['-C', repo, 'commit', '-m', 'fixture']); execFileSync('git', ['-C', repo, 'remote', 'add', 'origin', remote]); execFileSync('git', ['-C', repo, 'push', '-u', 'origin', 'main']);
    const tool = path.resolve('scripts/candidate-manifest.mjs');
    expect(() => execFileSync(process.execPath, [tool, 'generate'], { cwd: path.join(repo, 'desktop'), stdio: 'pipe' })).toThrow();
  });
});

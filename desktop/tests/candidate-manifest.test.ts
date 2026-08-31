import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterEach, describe, expect, it } from 'vitest';
import {
  assertCandidateManifest, assertRepositoryReady, buildCandidateManifest, CANONICAL_ORIGIN, expectedInstallerBasename, serializeCandidateManifest, verifyCandidateArtifacts,
} from '../scripts/candidate-manifest-lib.mjs';

// The candidate manifest is the offline provenance record for a hand-delivered
// Windows installer: what was built, from which commit, hashing to what.
//
// TWO THINGS CHANGE HERE, AND THEY PULL IN OPPOSITE DIRECTIONS.
//
//   The installer's NAME loses its version. It becomes
//   Void-Code-windows-x64.exe, because the download page links a GitHub
//   permalink with the asset name baked into it and a versioned name breaks
//   that link every release.
//
//   The manifest's RECORD of the version stops being optional. It used to come
//   from desktop/package.json -- 0.1.0, the placeholder -- so a candidate
//   manifest was a provenance record that stated the wrong version with a
//   straight face. It now comes from the private-runtime manifest sitting
//   beside the installer, which is written by the assembly that stamped the
//   build, so it is the version the artifact actually reports about itself.
//
// Losing the version from the file name is only affordable BECAUSE the manifest
// records it properly; the two changes are one change.

const roots: string[] = [];
const sha = (text: string) => createHash('sha256').update(text).digest('hex');
function temporary(prefix: string) { const root = mkdtempSync(path.join(os.tmpdir(), prefix)); roots.push(root); return root; }
// "No build block at all" needs a sentinel, not `undefined`: `undefined` is
// what a defaulted parameter is, so fixture(undefined) hands back the DEFAULT
// build block -- the valid one every other test in this file relies on. Written
// with `undefined` in the refusal list below, the case asked for the same
// fixture to be accepted on one line and refused on another, which no
// implementation can satisfy.
const NO_BUILD = Symbol('a runtime manifest with no build block at all');
function fixture(build: unknown = { version: '0.2.50', describe: 'v0.2.50' }) {
  const root = temporary('vc-candidate-');
  const installer = path.join(root, 'Void-Code-windows-x64.exe');
  const resources = path.join(root, 'manifest.json');
  writeFileSync(installer, 'exact unsigned installer fixture');
  const manifest: Record<string, unknown> = { schema: 1, platform: 'win32-x64', build, vc: {}, node: {}, pi: {} };
  if (build === NO_BUILD) delete manifest.build;
  writeFileSync(resources, JSON.stringify(manifest));
  return { root, installer, resources };
}
const input = (artifacts: { installer: string; resources: string }) => ({
  productName: 'Void Code', sourceCommit: '1'.repeat(40), sourceOrigin: CANONICAL_ORIGIN, arch: 'x64',
  installerPath: artifacts.installer, resourceManifestPath: artifacts.resources, buildTimestamp: '2026-07-27T12:34:56.000Z',
  predecessorReference: 'VC14-prototype-80487da8', predecessorSha256: 'a'.repeat(64),
  operatorGate: 'blocked', gateEvidence: 'VC-19', gateVerifiedAt: null,
});
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

describe('the installer is named for what it is, not for which build it is', () => {
  it('names the file without a version, for every architecture', () => {
    expect(expectedInstallerBasename('Void Code', 'x64')).toBe('Void-Code-windows-x64.exe');
    expect(expectedInstallerBasename('Void Code', 'arm64')).toBe('Void-Code-windows-arm64.exe');
  });

  it('refuses a product or architecture it does not know rather than inventing a name', () => {
    expect(() => expectedInstallerBasename('Something Else', 'x64')).toThrow();
    expect(() => expectedInstallerBasename('Void Code', 'mips')).toThrow();
  });
});

describe('candidate manifest provenance', () => {
  it('builds and validates one deterministic unsigned manifest from exact files', () => {
    const artifacts = fixture();
    const first = serializeCandidateManifest(buildCandidateManifest(input(artifacts)));
    const second = serializeCandidateManifest(buildCandidateManifest(input(artifacts)));
    expect(second).toBe(first);
    const manifest = assertCandidateManifest(JSON.parse(first));
    expect(manifest).toMatchObject({
      schema: 1, product: { name: 'Void Code', version: '0.2.50' }, source: { commit: '1'.repeat(40), branch: 'main', remote: 'origin/main', originUrl: CANONICAL_ORIGIN },
      installer: { basename: 'Void-Code-windows-x64.exe', sha256: sha('exact unsigned installer fixture'), arch: 'x64' },
      predecessor: { reference: 'VC14-prototype-80487da8', installerSha256: 'a'.repeat(64) }, signing: { status: 'unsigned' },
      operatorGate: { status: 'blocked', evidence: 'VC-19', verifiedAt: null },
    });
  });

  it('takes the version from the runtime manifest beside the installer, never from an argument', () => {
    // The caller cannot state a version. There is exactly one place the answer
    // can come from, and it is the file the build wrote -- which is what stops
    // a provenance record from carrying a version somebody typed.
    const artifacts = fixture({ version: '0.2.51-4-gdeadbee', describe: 'v0.2.51-4-gdeadbee' });
    const manifest = buildCandidateManifest(input(artifacts));
    expect(manifest.product.version).toBe('0.2.51-4-gdeadbee');
    expect(() => buildCandidateManifest({ ...input(artifacts), version: '9.9.9' })).toThrow();
  });

  it('refuses an unstamped or nonsensical runtime manifest instead of recording the placeholder', () => {
    for (const build of [NO_BUILD, {}, { version: '' }, { version: 'dev' }, { version: 'v0.2.50' }, { version: '0.2' }]) {
      const label = typeof build === 'symbol' ? build.description : JSON.stringify(build);
      expect(() => buildCandidateManifest(input(fixture(build))), `${label} was accepted as a build version`).toThrow();
    }
  });

  it('rejects dirty/diverged source and malformed or mutable inputs', () => {
    const clean = { branch: 'main', upstream: 'origin/main', originUrl: CANONICAL_ORIGIN, status: '', head: '1'.repeat(40), upstreamHead: '1'.repeat(40), remoteHead: '1'.repeat(40) };
    expect(assertRepositoryReady(clean)).toBe('1'.repeat(40));
    for (const changed of [
      { status: '?? secret.txt' }, { branch: 'pilot' }, { upstream: 'fork/main' }, { originUrl: 'https://github.com/attacker/fork.git' }, { upstreamHead: '2'.repeat(40) }, { remoteHead: '3'.repeat(40) },
    ]) expect(() => assertRepositoryReady({ ...clean, ...changed })).toThrow();
    const base = input(fixture());
    for (const changed of [
      { predecessorReference: 'latest' }, { predecessorSha256: 'abc' }, { buildTimestamp: 'now' }, { operatorGate: 'verified' }, { gateEvidence: 'pending' },
    ]) expect(() => buildCandidateManifest({ ...base, ...changed })).toThrow();
    expect(() => assertCandidateManifest({ ...buildCandidateManifest(base), token: 'forbidden' })).toThrow('unknown or missing');
  });

  it('validates a recorded version the way the build spells one', () => {
    // Widened from ^\d+\.\d+\.\d+$ deliberately: a build off the tag is a real
    // build and its version is a real version. Narrow enough that `dev`, a
    // leading `v` and a whole `--version` line are still refused.
    const manifest = buildCandidateManifest(input(fixture()));
    for (const version of ['0.2.50', '0.2.50-3-gabc1234', '1.0.0-rc.1', '0.0.0-gabc1234']) {
      expect(() => assertCandidateManifest({ ...manifest, product: { ...manifest.product, version } })).not.toThrow();
    }
    for (const version of ['v0.2.50', 'dev', '', '0.2', 'vc v0.2.50', '0.2.50 dirty']) {
      expect(() => assertCandidateManifest({ ...manifest, product: { ...manifest.product, version } }), `${version} was accepted`).toThrow();
    }
  });

  it('checks exact artifact bytes and the CLI rejects a clean synchronized fork', () => {
    const artifacts = fixture();
    const manifest = buildCandidateManifest(input(artifacts));
    expect(verifyCandidateArtifacts(manifest, artifacts.installer, artifacts.resources)).toBe(manifest);
    writeFileSync(artifacts.installer, 'changed installer');
    expect(() => verifyCandidateArtifacts(manifest, artifacts.installer, artifacts.resources)).toThrow('hash or size mismatch');

    const repo = temporary('vc-candidate-fork-'); const remote = temporary('vc-candidate-fork-remote-');
    execFileSync('git', ['init', '--bare', remote]); execFileSync('git', ['init', '-b', 'main', repo]);
    execFileSync('git', ['-C', repo, 'config', 'user.name', 'Fixture']); execFileSync('git', ['-C', repo, 'config', 'user.email', 'fixture@example.invalid']);
    mkdirSync(path.join(repo, 'desktop')); writeFileSync(path.join(repo, 'desktop/package.json'), JSON.stringify({ version: '0.1.0', build: { productName: 'Void Code', appId: 'works.voidcode.desktop', nsis: { artifactName: 'Void-Code-windows-${arch}.${ext}' } } }));
    execFileSync('git', ['-C', repo, 'add', '.']); execFileSync('git', ['-C', repo, 'commit', '-m', 'fixture']); execFileSync('git', ['-C', repo, 'remote', 'add', 'origin', remote]); execFileSync('git', ['-C', repo, 'push', '-u', 'origin', 'main']);
    const tool = path.resolve('scripts/candidate-manifest.mjs');
    expect(() => execFileSync(process.execPath, [tool, 'generate'], { cwd: path.join(repo, 'desktop'), stdio: 'pipe' })).toThrow();
  });

  it('the CLI reads the package identity that exists, so a stale expectation cannot pass it', () => {
    // scripts/candidate-manifest.mjs asserts the authoritative identity before
    // it writes anything, and it asserted the OLD artifactName. Left as it was,
    // it would refuse every real tree from the day the name changes.
    const cli = readFileSync(new URL('../scripts/candidate-manifest.mjs', import.meta.url), 'utf8');
    const packageJson = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8')) as { build: { nsis: { artifactName: string } } };
    expect(cli, 'candidate-manifest.mjs expects an artifactName this tree does not have').toContain(packageJson.build.nsis.artifactName);
    expect(cli, 'candidate-manifest.mjs still takes the candidate version from package.json').not.toMatch(/version:\s*packageJson\.version/);
  });
});

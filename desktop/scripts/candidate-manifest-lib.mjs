import { createHash } from 'node:crypto';
import { lstatSync, readFileSync } from 'node:fs';
import path from 'node:path';

export const SHA256 = /^[a-f0-9]{64}$/;
export const COMMIT = /^[a-f0-9]{40}$/;
export const CANONICAL_ORIGIN = 'https://github.com/makscee/void-code.git';
const REFERENCE = /^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$/;
const MUTABLE_REFERENCE = /^(latest|current|head|pending|unknown|tbd|none)$/i;
const ISO_UTC = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/;

export function sha256File(file) { return createHash('sha256').update(readFileSync(file)).digest('hex'); }
export function inspectFile(file, expectedBasename) {
  const info = lstatSync(file);
  if (!info.isFile() || info.isSymbolicLink() || info.size < 1) throw new Error(`${expectedBasename} must be one non-empty regular file`);
  if (path.basename(file) !== expectedBasename) throw new Error(`artifact name mismatch: expected ${expectedBasename}`);
  return { basename: expectedBasename, size: info.size, sha256: sha256File(file) };
}
export function assertCanonicalTimestamp(value, label) {
  if (!ISO_UTC.test(value) || new Date(value).toISOString() !== value) throw new Error(`${label} must be canonical UTC ISO-8601 with milliseconds`);
  return value;
}
export function assertReference(value, label) {
  if (!REFERENCE.test(value) || MUTABLE_REFERENCE.test(value)) throw new Error(`${label} must be an immutable reference label`);
  return value;
}
export function canonicalOrigin(value) {
  if (value === CANONICAL_ORIGIN || value === 'git@github.com:makscee/void-code.git' || value === 'ssh://git@github.com/makscee/void-code.git') return CANONICAL_ORIGIN;
  throw new Error('candidate origin is not the canonical void-code repository');
}
export function assertRepositoryReady(facts) {
  canonicalOrigin(facts.originUrl);
  if (facts.branch !== 'main') throw new Error('candidate source branch must be main');
  if (facts.upstream !== 'origin/main') throw new Error('candidate upstream must be origin/main');
  if (facts.status !== '') throw new Error('candidate source tree must be clean, including untracked files');
  for (const [label, value] of [['HEAD', facts.head], ['upstream', facts.upstreamHead], ['remote', facts.remoteHead]]) if (!COMMIT.test(value)) throw new Error(`${label} commit is malformed`);
  if (facts.head !== facts.upstreamHead || facts.head !== facts.remoteHead) throw new Error('candidate source is diverged or remote state is unresolved');
  return facts.head;
}
export function expectedInstallerBasename(productName, version, arch) {
  if (productName !== 'Void Code' || !/^\d+\.\d+\.\d+$/.test(version) || (arch !== 'x64' && arch !== 'arm64')) throw new Error('unsupported product identity or architecture');
  return `Void-Code-${version}-windows-${arch}.exe`;
}
function exactKeys(value, keys, label) {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) throw new Error(`${label} must be an object`);
  const actual = Object.keys(value).sort(); const expected = [...keys].sort();
  if (actual.length !== expected.length || actual.some((key, index) => key !== expected[index])) throw new Error(`${label} has unknown or missing fields`);
}
export function buildCandidateManifest(input) {
  const installer = inspectFile(input.installerPath, expectedInstallerBasename(input.productName, input.version, input.arch));
  const resource = inspectFile(input.resourceManifestPath, 'manifest.json');
  const resourceValue = JSON.parse(readFileSync(input.resourceManifestPath, 'utf8'));
  if (resourceValue.schema !== 1 || resourceValue.platform !== `win32-${input.arch}`) throw new Error('Windows private-runtime manifest identity mismatch');
  if (!SHA256.test(input.predecessorSha256)) throw new Error('predecessor SHA-256 is malformed');
  assertReference(input.predecessorReference, 'predecessor reference');
  assertReference(input.gateEvidence, 'operator gate evidence');
  if (input.operatorGate !== 'blocked' && input.operatorGate !== 'verified') throw new Error('operator gate must be blocked or verified');
  if (input.operatorGate === 'verified') assertCanonicalTimestamp(input.gateVerifiedAt, 'operator verified timestamp');
  else if (input.gateVerifiedAt !== null) throw new Error('blocked operator gate cannot have a verified timestamp');
  assertCanonicalTimestamp(input.buildTimestamp, 'build timestamp');
  if (!COMMIT.test(input.sourceCommit)) throw new Error('source commit is malformed');
  return {
    schema: 1,
    product: { name: input.productName, version: input.version },
    source: { commit: input.sourceCommit, branch: 'main', remote: 'origin/main', originUrl: canonicalOrigin(input.sourceOrigin) },
    build: { timestamp: input.buildTimestamp },
    installer: { ...installer, arch: input.arch },
    resources: { manifest: resource, platform: resourceValue.platform },
    predecessor: { reference: input.predecessorReference, installerSha256: input.predecessorSha256 },
    signing: { status: 'unsigned' },
    operatorGate: { status: input.operatorGate, evidence: input.gateEvidence, verifiedAt: input.gateVerifiedAt },
  };
}
export function assertCandidateManifest(manifest) {
  exactKeys(manifest, ['schema', 'product', 'source', 'build', 'installer', 'resources', 'predecessor', 'signing', 'operatorGate'], 'manifest');
  exactKeys(manifest.product, ['name', 'version'], 'product'); exactKeys(manifest.source, ['commit', 'branch', 'remote', 'originUrl'], 'source');
  exactKeys(manifest.build, ['timestamp'], 'build'); exactKeys(manifest.installer, ['basename', 'size', 'sha256', 'arch'], 'installer');
  exactKeys(manifest.resources, ['manifest', 'platform'], 'resources'); exactKeys(manifest.resources.manifest, ['basename', 'size', 'sha256'], 'resource manifest');
  exactKeys(manifest.predecessor, ['reference', 'installerSha256'], 'predecessor'); exactKeys(manifest.signing, ['status'], 'signing');
  exactKeys(manifest.operatorGate, ['status', 'evidence', 'verifiedAt'], 'operator gate');
  if (manifest.schema !== 1 || manifest.product.name !== 'Void Code' || !/^\d+\.\d+\.\d+$/.test(manifest.product.version)) throw new Error('manifest product identity invalid');
  if (!COMMIT.test(manifest.source.commit) || manifest.source.branch !== 'main' || manifest.source.remote !== 'origin/main' || canonicalOrigin(manifest.source.originUrl) !== CANONICAL_ORIGIN) throw new Error('manifest source invalid');
  assertCanonicalTimestamp(manifest.build.timestamp, 'build timestamp');
  if (manifest.installer.basename !== expectedInstallerBasename(manifest.product.name, manifest.product.version, manifest.installer.arch) || !Number.isInteger(manifest.installer.size) || manifest.installer.size < 1 || !SHA256.test(manifest.installer.sha256)) throw new Error('manifest installer invalid');
  if (manifest.resources.manifest.basename !== 'manifest.json' || !Number.isInteger(manifest.resources.manifest.size) || manifest.resources.manifest.size < 1 || !SHA256.test(manifest.resources.manifest.sha256) || manifest.resources.platform !== `win32-${manifest.installer.arch}`) throw new Error('manifest resources invalid');
  assertReference(manifest.predecessor.reference, 'predecessor reference'); if (!SHA256.test(manifest.predecessor.installerSha256)) throw new Error('manifest predecessor hash invalid');
  if (manifest.signing.status !== 'unsigned') throw new Error('candidate must remain unsigned');
  assertReference(manifest.operatorGate.evidence, 'operator gate evidence');
  if (manifest.operatorGate.status === 'verified') assertCanonicalTimestamp(manifest.operatorGate.verifiedAt, 'operator verified timestamp');
  else if (manifest.operatorGate.status !== 'blocked' || manifest.operatorGate.verifiedAt !== null) throw new Error('manifest operator gate invalid');
  return manifest;
}
export function verifyCandidateArtifacts(manifest, installerPath, resourceManifestPath) {
  assertCandidateManifest(manifest);
  const installer = inspectFile(installerPath, manifest.installer.basename);
  const resources = inspectFile(resourceManifestPath, 'manifest.json');
  if (JSON.stringify(installer) !== JSON.stringify({ basename: manifest.installer.basename, size: manifest.installer.size, sha256: manifest.installer.sha256 })) throw new Error('installer hash or size mismatch');
  if (JSON.stringify(resources) !== JSON.stringify(manifest.resources.manifest)) throw new Error('resource manifest hash or size mismatch');
  const resourceValue = JSON.parse(readFileSync(resourceManifestPath, 'utf8'));
  if (resourceValue.schema !== 1 || resourceValue.platform !== manifest.resources.platform) throw new Error('resource manifest platform mismatch');
  return manifest;
}
export function serializeCandidateManifest(manifest) { assertCandidateManifest(manifest); return `${JSON.stringify(manifest, null, 2)}\n`; }

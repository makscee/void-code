import { PACKAGE_VERSION } from './build-version.mjs';

export function normalizeAsarEntry(entry) {
  return entry.replaceAll('\\', '/');
}

export function startupFailureTimeoutMs(windows) {
  return windows ? 600_000 : 20_000;
}

// ---------------------------------------------------------------------------
// The two rules the packaged checks share.
//
// scripts/packaged-smoke.mjs (macOS) and scripts/windows-package-check.mjs
// (Windows) are the only places that look at a real installed artifact, and
// both used to compare it against a literal typed into the script --
// `identity.version !== '0.1.0'`. A build that finally reported its true
// version would have turned the smoke red for being correct.
//
// The comparison that survives a version change is the internal one: the bundle
// states a version, the private-runtime manifest packaged inside it records the
// version of the build that produced it, and the two must be the same string.
// Nothing is typed anywhere, and a stamp that reached one half but not the
// other is caught.
// ---------------------------------------------------------------------------

function buildVersionOf(manifest) {
  const build = manifest?.build;
  if (typeof build !== 'object' || build === null || Array.isArray(build)) throw new Error('the private runtime manifest records no build, so the packaged version cannot be checked');
  if (typeof build.version !== 'string' || !PACKAGE_VERSION.test(build.version)) throw new Error(`the private runtime manifest records no usable build version: ${JSON.stringify(build.version)}`);
  if (typeof build.describe !== 'string' || build.describe.trim() === '') throw new Error('the private runtime manifest records no build describe');
  return build;
}

/**
 * The packaged bundle's identity: the name it was always held to, and a version
 * that has to equal the one the assembly wrote into the manifest inside it.
 */
export function assertPackagedIdentity(identity, manifest) {
  const build = buildVersionOf(manifest);
  if (identity?.productName !== 'Void Code' || identity?.displayName !== 'Void Code' || identity?.appId !== 'works.voidcode.desktop') throw new Error(`packaged identity mismatch: ${JSON.stringify(identity)}`);
  if (typeof identity.version !== 'string' || !PACKAGE_VERSION.test(identity.version)) throw new Error(`packaged version is not a version: ${JSON.stringify(identity?.version)}`);
  if (identity.version !== build.version) throw new Error(`packaged version ${identity.version} disagrees with the build version ${build.version} recorded in its own private runtime manifest`);
  return identity;
}

/**
 * The vc inside the bundle knows which vc it is. `vc dev` is the regression
 * itself: the right binary, built without the ldflags that name it.
 */
export function assertStampedVc(versionLine, manifest) {
  const build = buildVersionOf(manifest);
  if (typeof versionLine !== 'string' || versionLine.trim() === '') throw new Error(`the packaged vc reported no version line: ${JSON.stringify(versionLine)}`);
  const expected = `vc ${build.describe}`;
  if (versionLine.trim() !== expected) throw new Error(`the packaged vc reports ${JSON.stringify(versionLine)}, but this build stamped ${JSON.stringify(expected)}`);
  if (typeof manifest.vc?.version === 'string' && manifest.vc.version.trim() !== expected) throw new Error(`the private runtime manifest recorded ${JSON.stringify(manifest.vc.version)} for vc, but this build stamped ${JSON.stringify(expected)}`);
  return versionLine;
}

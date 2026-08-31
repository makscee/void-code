import { describe, expect, it } from 'vitest';
import { assertPackagedIdentity, assertStampedVc, normalizeAsarEntry, startupFailureTimeoutMs } from '../scripts/packaged-check-lib.mjs';

describe('packaged check helpers', () => {
  it('normalizes asar entry separators on Windows and POSIX', () => {
    expect(normalizeAsarEntry('\\dist\\renderer\\index.html')).toBe('/dist/renderer/index.html');
    expect(normalizeAsarEntry('/dist/renderer/index.html')).toBe('/dist/renderer/index.html');
  });

  it('allows Windows runtime validation to finish before timing out startup failure checks', () => {
    expect(startupFailureTimeoutMs(false)).toBe(20_000);
    expect(startupFailureTimeoutMs(true)).toBe(600_000);
  });
});

// ---------------------------------------------------------------------------
// The two rules the packaged checks share, extracted so they can be measured
// here rather than only executed on a machine with a built bundle.
//
// scripts/packaged-smoke.mjs (macOS) and scripts/windows-package-check.mjs
// (Windows) are the only two places that ever look at a REAL installed
// artifact. Both used to compare it against a literal typed into the script --
// `identity.version !== '0.1.0'` -- which is how a build that finally reported
// its true version would have turned the smoke red for being correct.
//
// The comparison that survives a version change is the internal one: the app
// says a version, the private-runtime manifest packaged inside it records the
// version of the build that produced it, and the two must be the same string.
// Nothing has to be typed anywhere, and a stamp that reached one half but not
// the other -- which is exactly the state the tree is in today, with a
// correctly named release around a `vc dev` binary -- is caught.
// ---------------------------------------------------------------------------

const manifest = (build: unknown) => ({ schema: 1, platform: 'darwin-arm64', build, vc: { version: 'vc v0.2.50' }, node: {}, pi: {}, fixture: {} });
const identity = { productName: 'Void Code', displayName: 'Void Code', version: '0.2.50', appId: 'works.voidcode.desktop' };

describe('assertPackagedIdentity: the bundle and the manifest inside it say the same version', () => {
  it('accepts a bundle whose version is the one its manifest records', () => {
    expect(() => assertPackagedIdentity(identity, manifest({ version: '0.2.50', describe: 'v0.2.50' }))).not.toThrow();
  });

  it('accepts a branch build, prerelease and all', () => {
    expect(() => assertPackagedIdentity(
      { ...identity, version: '0.2.50-3-gabc1234' },
      manifest({ version: '0.2.50-3-gabc1234', describe: 'v0.2.50-3-gabc1234' }),
    )).not.toThrow();
  });

  it('refuses a bundle that disagrees with its own manifest', () => {
    // The half-landed stamp: electron-builder got the version, the assembly
    // did not, or the other way round.
    expect(() => assertPackagedIdentity(identity, manifest({ version: '0.2.49', describe: 'v0.2.49' }))).toThrow(/version/i);
  });

  it('refuses a bundle still carrying the placeholder', () => {
    expect(() => assertPackagedIdentity({ ...identity, version: '0.1.0' }, manifest({ version: '0.2.50', describe: 'v0.2.50' }))).toThrow(/version/i);
  });

  it('refuses a manifest with no build block, so an unstamped assembly cannot pass', () => {
    // Without this, dropping the build block from the assembly turns the check
    // off instead of failing it -- the check would have nothing to compare
    // against and would have to either invent a rule or wave the bundle
    // through. It fails.
    expect(() => assertPackagedIdentity(identity, manifest(undefined))).toThrow();
    expect(() => assertPackagedIdentity(identity, manifest({ describe: 'v0.2.50' }))).toThrow();
    expect(() => assertPackagedIdentity(identity, manifest({ version: '', describe: '' }))).toThrow();
  });

  it('refuses a manifest version that is not a version at all', () => {
    for (const bogus of ['dev', 'v0.2.50', 'latest', '0.2', 'vc v0.2.50']) {
      expect(() => assertPackagedIdentity({ ...identity, version: bogus }, manifest({ version: bogus, describe: bogus })), `${bogus} was accepted as a packaged version`).toThrow();
    }
  });

  it('still holds the name to account', () => {
    // The name half of the identity did not become negotiable just because the
    // version half moved.
    for (const wrong of [{ productName: 'Void' }, { displayName: 'void code' }, { appId: 'works.voidcode.other' }]) {
      expect(() => assertPackagedIdentity({ ...identity, ...wrong }, manifest({ version: '0.2.50', describe: 'v0.2.50' }))).toThrow();
    }
  });
});

describe('assertStampedVc: the vc inside the bundle knows which vc it is', () => {
  it('accepts the version line the manifest recorded for it', () => {
    expect(() => assertStampedVc('vc v0.2.50', manifest({ version: '0.2.50', describe: 'v0.2.50' }))).not.toThrow();
  });

  it('refuses `vc dev`, which is the regression itself', () => {
    // Shipped inside the v0.2.50 Windows installer: a vc built from the right
    // tree with no ldflags. Everything about it worked except being able to say
    // what it was.
    expect(() => assertStampedVc('vc dev', { ...manifest({ version: '0.2.50', describe: 'v0.2.50' }), vc: { version: 'vc dev' } })).toThrow(/dev/);
  });

  it('refuses a vc that disagrees with the manifest that hashed it', () => {
    expect(() => assertStampedVc('vc v0.2.49', manifest({ version: '0.2.50', describe: 'v0.2.50' }))).toThrow();
  });

  it('refuses an empty or absent version line rather than reading it as agreement', () => {
    for (const line of ['', '   ', undefined, null]) {
      expect(() => assertStampedVc(line as string, manifest({ version: '0.2.50', describe: 'v0.2.50' }))).toThrow();
    }
  });
});

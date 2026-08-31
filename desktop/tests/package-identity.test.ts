import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const read = (relative: string): string => readFileSync(new URL(`../${relative}`, import.meta.url), 'utf8');

// This file used to say the pilot's identity was "Void Code 0.1.0", and it was
// right about the tree and wrong about the world: 0.1.0 has been the version in
// desktop/package.json since 2026-07-25 and the version of nothing anybody ever
// installed. It named the installer, the packaged bundle and the support
// report, while the release it came from was v0.2.50.
//
// The identity splits in two, and the split is the point:
//
//   NAME     Void Code, works.voidcode.desktop, Void-Code-windows-x64.exe.
//            Fixed, in the tree, asserted here character for character. It is
//            what the thing IS.
//   VERSION  stamped at build time from `git describe --tags --always` and
//            never written into the tree. It is what the build IS, and the
//            tree cannot know it -- a checkout has no idea which tag it will
//            be released under.
//
// So package.json's `version` field stops being an assertion about anything.
// It is the placeholder electron-builder needs present, and every consumer that
// used to read it now reads the stamp instead. What this file pins is that no
// consumer went back to reading it.
//
// THE FILE NAME LOST ITS VERSION, AND THAT IS DELIBERATE. The download page
// links `releases/latest/download/Void-Code-windows-x64.exe` -- a GitHub
// permalink with the asset name baked in. An installer named for its version
// breaks that link at every release and hands the manual work back. The name
// is identity, the version is content; keeping them apart is what lets the link
// stay permanent AND the version stay honest.

describe('decided pilot package identity', () => {
  it('fixes the name in the tree: product, app id and installer', () => {
    const packageJson = JSON.parse(read('package.json')) as {
      version: string;
      description: string;
      build: { appId: string; productName: string; nsis: { artifactName: string } };
    };
    expect(packageJson).toMatchObject({
      description: 'Guided Void Code desktop pilot',
      build: {
        appId: 'works.voidcode.desktop',
        productName: 'Void Code',
        nsis: { artifactName: 'Void-Code-windows-${arch}.${ext}' },
      },
    });
    expect(read('src/renderer/index.html')).toContain('<title>Void Code</title>');
  });

  it('keeps the version out of the installer name, so the download permalink survives a release', () => {
    const packageJson = JSON.parse(read('package.json')) as { build: { nsis: { artifactName: string } } };
    // Stated as the property rather than as the string above, so that a later
    // pattern change cannot put ${version} back while still looking tidy.
    expect(packageJson.build.nsis.artifactName, 'the installer name carries the version again, which breaks releases/latest/download/...').not.toContain('${version}');
  });

  it('leaves package.json version as a placeholder nobody is entitled to read', () => {
    const packageJson = JSON.parse(read('package.json')) as { version: string };
    const packageLock = JSON.parse(read('package-lock.json')) as { version: string; packages: Record<string, { version?: string }> };
    // Not bumped by hand, not bumped by the build. It stays as it is, and the
    // rest of this file is about it staying unread. Asserting the exact value
    // keeps an accidental `npm version` visible.
    expect(packageJson.version).toBe('0.1.0');
    expect(packageLock.version).toBe('0.1.0');
    expect(packageLock.packages['']?.version).toBe('0.1.0');
  });

  it('keeps package consumers deterministic without changing private runtime identity', () => {
    const privateRuntime = JSON.parse(read('runtime/pi/package.json')) as { name: string; version: string };
    expect(privateRuntime).toEqual(expect.objectContaining({ name: 'void-code-private-pi-runtime', version: '0.0.1' }));

    const macPtyCheck = read('scripts/mac-pty-check.mjs');
    expect(macPtyCheck).toContain("release/mac-arm64/Void Code.app");
    expect(macPtyCheck).toContain("const electronVersion = packageJson.devDependencies?.electron");
    expect(macPtyCheck).toContain("installedElectronVersion !== electronVersion");
    expect(macPtyCheck).toContain("electron: electronVersion");
    expect(macPtyCheck).not.toMatch(/electron:\s*['"]\d+\.\d+\.\d+['"]/);
    expect(read('scripts/production-terminal-check.mjs')).toContain("release/mac-arm64/Void Code.app/Contents/MacOS/Void Code");
  });

  it('names the installer without a version wherever a script has to name it', () => {
    expect(read('scripts/windows-package-check.mjs')).toContain("Void-Code-windows-x64.exe");
  });

  it('has no consumer left comparing a packaged artifact against 0.1.0', () => {
    // scripts/packaged-smoke.mjs used to fail the smoke unless the packaged
    // bundle said 0.1.0 -- so the day the stamp starts working, the smoke would
    // have gone red for being right. Every such literal is a check that
    // enforces the lie.
    const consumers = ['scripts/packaged-smoke.mjs', 'scripts/windows-package-check.mjs', 'scripts/candidate-manifest.mjs', 'scripts/mac-pty-check.mjs', 'scripts/production-terminal-check.mjs'];
    const offenders = consumers.filter((file) => /['"]0\.1\.0['"]/.test(read(file)));
    expect(offenders.join(', ') || 'no packaged check compares against the placeholder').toBe('no packaged check compares against the placeholder');
  });

  it('checks the packaged version against the manifest the bundle ships, not against a literal', () => {
    // The only honest comparison available to a script looking at a built
    // bundle: the app says one version, the private runtime manifest inside it
    // records another, and they must be the same string or the stamp did not
    // reach both. The rule itself is pure and is measured in
    // packaged-check-lib.test.ts; here it is only pinned as used.
    const smoke = read('scripts/packaged-smoke.mjs');
    expect(smoke).toContain("release/mac-arm64/Void Code.app");
    expect(smoke, 'packaged-smoke.mjs does not import the shared identity rule').toMatch(/from '\.\/packaged-check-lib\.mjs'/);
    expect(smoke, 'packaged-smoke.mjs does not call assertPackagedIdentity').toMatch(/assertPackagedIdentity\s*\(/);
  });

  it('makes the Windows check refuse an unstamped vc', () => {
    // The Windows path has no Info.plist to read, but it already runs
    // `vc --version` out of the packaged runtime -- which is precisely the
    // value that regressed to `vc dev`. It is the cheapest place to catch the
    // original defect on the platform where it shipped.
    const check = read('scripts/windows-package-check.mjs');
    expect(check, 'windows-package-check.mjs does not import the shared identity rule').toMatch(/from '\.\/packaged-check-lib\.mjs'/);
    expect(check, 'windows-package-check.mjs does not call assertStampedVc').toMatch(/assertStampedVc\s*\(/);
  });
});

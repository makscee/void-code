import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { afterAll, describe, expect, it } from 'vitest';
import {
  GO_VERSION_SYMBOL, PACKAGE_VERSION, electronBuilderVersionArgs, parseBuildVersion, readBuildVersion, vcBuildArgs,
} from '../scripts/build-version.mjs';

// One question -- "which version is this?" -- had four different answers, and
// none of them was the release the person downloaded:
//
//   manifest vc.version        vc dev          (go build with no ldflags)
//   desktop/package.json       0.1.0           (untouched since 2026-07-25)
//   installer file name        Void-Code-0.1.0-windows-x64.exe
//   the interface              nothing at all
//
// while the release was v0.2.50. The first line is ours: the Windows assembly
// used to copy a vc out of a past release -- wrong binary, but stamped by
// release.yml's `-ldflags "-X ...version.Version=<tag>"` -- and we replaced it
// with a build from source and did not carry the ldflags over.
//
// The fix is not four fixes. There is one source of truth -- `git describe
// --tags --always` at build time -- and one module that turns it into the three
// spellings its three consumers need. This file tests that module.
//
// TWO SPELLINGS, AND WHY THEY ARE NOT ONE:
//
//   stamp           what git said, unchanged: `v0.2.50`, or `v0.2.50-3-gabc1234`
//                   off a tag. It goes into ldflags (so `vc --version` prints
//                   what release.yml would have printed) and into the manifest.
//   packageVersion  the same thing as semver: `0.2.50`. electron-builder puts
//                   this in the bundle, so it must satisfy semver or the
//                   packer refuses it -- a leading `v` is not semver.
//
// They differ only by a documented normalization, and both are derived from the
// one string, which is what stops them drifting the way the four answers above
// did. The functions take the PARSED OBJECT rather than a string, so a caller
// cannot reach for the wrong spelling by accident -- passing a bare string is
// refused below.
//
// No version literal from today's tree is asserted anywhere in this file. The
// tag moves; what is checked is the RELATION -- that what the build stamps is
// what git says -- so this suite stays honest at v0.3.0.

const repo = path.resolve('..');
const temporaries: string[] = [];
const temporary = (prefix: string) => { const root = mkdtempSync(path.join(os.tmpdir(), prefix)); temporaries.push(root); return root; };
afterAll(() => { for (const root of temporaries.splice(0)) rmSync(root, { recursive: true, force: true }); });

// ---------------------------------------------------------------------------
// The ldflags target is a real symbol, not a string that once was one.
//
// `-X pkg.Var=value` is silently inert when `pkg` or `Var` does not exist: the
// build succeeds and the binary still says `dev`. That is the exact failure
// this whole file exists to prevent, so the symbol is checked against the two
// files that define it rather than being trusted as a constant.
// ---------------------------------------------------------------------------

describe('the ldflags symbol names the variable Go actually has', () => {
  const goMod = readFileSync(path.join(repo, 'go.mod'), 'utf8');
  const versionSource = readFileSync(path.join(repo, 'internal/version/version.go'), 'utf8');

  it('is the module path, the package directory and the exported variable, composed', () => {
    const module = /^module\s+(\S+)$/m.exec(goMod)?.[1];
    expect(module ?? 'go.mod declares no module path').toMatch(/^\S+$/);
    expect(versionSource, 'internal/version/version.go no longer declares `var Version`').toMatch(/^var Version\b/m);
    expect(versionSource, 'internal/version/version.go is no longer package version').toMatch(/^package version$/m);
    expect(GO_VERSION_SYMBOL).toBe(`${module}/internal/version.Version`);
  });

  it('is the same symbol release.yml has always stamped, so a desktop build and a release binary agree', () => {
    // The CLI half of a release already does this correctly. Copying the
    // spelling from the workflow rather than inventing one is what makes
    // `vc --version` inside the app equal `vc --version` from the release page.
    const release = readFileSync(path.join(repo, '.github/workflows/release.yml'), 'utf8');
    expect(release).toContain(`-X ${GO_VERSION_SYMBOL}=`);
  });
});

// ---------------------------------------------------------------------------
// Normalization, stated as a table.
// ---------------------------------------------------------------------------

describe('parseBuildVersion turns one git answer into the spellings its consumers need', () => {
  const table: ReadonlyArray<readonly [string, string, string]> = [
    // described                  stamp                      packageVersion
    ['v0.2.50', 'v0.2.50', '0.2.50'],
    ['0.2.50', '0.2.50', '0.2.50'],
    // Off the tag: `git describe` counts the commits and names the commit. It
    // is already valid semver once the `v` is gone -- `3-gabc1234` is one
    // prerelease identifier -- and it SHOULD reach the user, because a branch
    // build must not be able to claim it is the release.
    ['v0.2.50-3-gabc1234', 'v0.2.50-3-gabc1234', '0.2.50-3-gabc1234'],
    ['v1.0.0-rc.1', 'v1.0.0-rc.1', '1.0.0-rc.1'],
    // `--always` with no tag in reach: a bare commit. 0.0.0 sorts below every
    // real release and reads as "not a release" at a glance, and the commit is
    // kept so a support report still identifies the build.
    ['abc1234', 'abc1234', '0.0.0-gabc1234'],
    ['0123456789abcdef0123456789abcdef01234567', '0123456789abcdef0123456789abcdef01234567', '0.0.0-g0123456789abcdef0123456789abcdef01234567'],
  ];

  it.each(table)('reads %s as stamp %s and package version %s', (described, stamp, packageVersion) => {
    expect(parseBuildVersion(described)).toEqual({ described, stamp, packageVersion });
  });

  it('trims what a command substitution leaves behind', () => {
    expect(parseBuildVersion('  v0.2.50\n').stamp).toBe('v0.2.50');
  });

  it('produces a package version electron-builder will accept, for every row above', () => {
    for (const [described] of table) expect(parseBuildVersion(described).packageVersion).toMatch(PACKAGE_VERSION);
  });

  const refusals: ReadonlyArray<readonly [string, unknown]> = [
    ['the sentinel of an unstamped build', 'dev'],
    ['a whole --version line rather than a version', 'vc v0.2.50'],
    ['nothing at all', ''],
    ['whitespace', '   '],
    ['a value with a space in it', 'v0.2.50 dirty'],
    ['a branch name', 'work/desktop-version'],
    ['a number', 250],
    ['nothing', undefined],
    ['null', null],
  ];

  it.each(refusals)('refuses %s rather than inventing a version', (_name, value) => {
    // Refusing is the point. A module that answered 0.1.0 here would put the
    // lie back exactly where it was, and the build would stay green while doing
    // it -- which is how the four wrong answers survived five weeks.
    expect(() => parseBuildVersion(value as string)).toThrow();
  });
});

// ---------------------------------------------------------------------------
// Where the answer comes from, and that it is really git being asked.
// ---------------------------------------------------------------------------

describe('readBuildVersion asks git, and asks it the documented question', () => {
  it('runs `describe --tags --always` and nothing else', () => {
    const asked: string[][] = [];
    const version = readBuildVersion((args: string[]) => { asked.push(args); return 'v9.9.9\n'; });
    expect(asked).toEqual([['describe', '--tags', '--always']]);
    expect(version).toEqual({ described: 'v9.9.9', stamp: 'v9.9.9', packageVersion: '9.9.9' });
  });

  it('lets a git failure fail the build rather than falling back to a made-up version', () => {
    expect(() => readBuildVersion(() => { throw new Error('not a git repository'); })).toThrow('not a git repository');
  });

  it('agrees with this repository, whatever the tag happens to be today', () => {
    // The RELATION, not a literal: the module and the command line are asked
    // the same question and must give the same answer. When the tag moves to
    // v0.3.0 this test keeps meaning what it means.
    const measured = execFileSync('git', ['-C', repo, 'describe', '--tags', '--always'], { encoding: 'utf8' }).trim();
    expect(readBuildVersion().described).toBe(measured);
    expect(readBuildVersion().stamp).toBe(measured);
  });
});

// ---------------------------------------------------------------------------
// The go build argv -- and then the binary, for real.
// ---------------------------------------------------------------------------

describe('vcBuildArgs stamps the build without changing what else the build is', () => {
  const version = parseBuildVersion('v9.9.9-3-gfeedface');

  it('carries the ldflags, and keeps -trimpath and -buildvcs=false', () => {
    // Both assemblies build with -trimpath -buildvcs=false today. Those are not
    // decoration: -trimpath keeps the shipped binary free of this machine's
    // paths, and -buildvcs=false keeps a dirty worktree from changing the
    // bytes. Adding a stamp must not quietly drop either.
    const args = vcBuildArgs('/tmp/out/vc', version);
    expect(args[0]).toBe('build');
    expect(args).toContain('-trimpath');
    expect(args).toContain('-buildvcs=false');
    expect(args).toContain('./cmd/vc');
    expect(args[args.indexOf('-o') + 1]).toBe('/tmp/out/vc');
    expect(args[args.indexOf('-ldflags') + 1]).toBe(`-X ${GO_VERSION_SYMBOL}=${version.stamp}`);
  });

  it('stamps the stamp, never the semver spelling', () => {
    // `vc --version` inside the app has to be comparable with `vc --version`
    // from a release binary, and release.yml stamps the tag as git spells it.
    const args = vcBuildArgs('/tmp/out/vc', parseBuildVersion('v0.2.50'));
    expect(args).toContain(`-X ${GO_VERSION_SYMBOL}=v0.2.50`);
    expect(args.join(' ')).not.toContain('=0.2.50 ');
  });

  it('refuses a bare string, so no caller can hand it the wrong spelling', () => {
    expect(() => vcBuildArgs('/tmp/out/vc', 'v0.2.50' as unknown as ReturnType<typeof parseBuildVersion>)).toThrow();
    expect(() => vcBuildArgs('/tmp/out/vc', { stamp: 'dev' } as unknown as ReturnType<typeof parseBuildVersion>)).toThrow();
  });

  // -------------------------------------------------------------------------
  // The one measurement in this file. Everything above is about strings; this
  // compiles the real cmd/vc with those strings and asks the binary.
  //
  // It needs a Go toolchain, and it does not skip when there is none: `go test
  // ./...` is this repository's definition of green, so a machine running this
  // suite has one. A skipped check that reports as a pass is how a gate ends up
  // certifying nothing.
  // -------------------------------------------------------------------------
  const go = (args: string[], destination: string) => {
    execFileSync('go', args, { cwd: repo, env: { ...process.env, CGO_ENABLED: '0' }, stdio: 'pipe' });
    return execFileSync(destination, ['--version'], { encoding: 'utf8' }).trim();
  };

  it('produces a vc that prints the stamped version', { timeout: 300_000 }, () => {
    const destination = path.join(temporary('vc-stamp-'), 'vc');
    expect(go(vcBuildArgs(destination, version), destination)).toBe(`vc ${version.stamp}`);
  });

  it('and the same build without the ldflags prints `vc dev`, which is the bug being fixed', { timeout: 300_000 }, () => {
    // The control. Without it the test above could pass because `vc --version`
    // happens to print something, rather than because the stamp arrived.
    const destination = path.join(temporary('vc-unstamped-'), 'vc');
    expect(go(['build', '-trimpath', '-buildvcs=false', '-o', destination, './cmd/vc'], destination)).toBe('vc dev');
  });
});

// ---------------------------------------------------------------------------
// electron-builder gets the version on the command line, not in the tree.
// ---------------------------------------------------------------------------

describe('electronBuilderVersionArgs hands the packer a version without editing the tree', () => {
  it('passes the semver spelling through -c.extraMetadata.version', () => {
    expect(electronBuilderVersionArgs(parseBuildVersion('v0.2.50'))).toEqual(['-c.extraMetadata.version=0.2.50']);
  });

  it('passes a branch build through as a prerelease rather than rounding it to the tag', () => {
    // Rounding `0.2.50-3-gabc1234` to `0.2.50` would let a branch build claim
    // to be the release -- the same lie as `0.1.0`, one digit closer.
    expect(electronBuilderVersionArgs(parseBuildVersion('v0.2.50-3-gabc1234'))).toEqual(['-c.extraMetadata.version=0.2.50-3-gabc1234']);
  });

  it('never hands over the leading v, which electron-builder would reject as non-semver', () => {
    for (const described of ['v0.2.50', 'v0.2.50-3-gabc1234', 'v1.0.0-rc.1']) {
      const [argument] = electronBuilderVersionArgs(parseBuildVersion(described));
      expect(argument.slice('-c.extraMetadata.version='.length)).toMatch(PACKAGE_VERSION);
    }
  });

  it('refuses a bare string here too', () => {
    expect(() => electronBuilderVersionArgs('0.2.50' as unknown as ReturnType<typeof parseBuildVersion>)).toThrow();
  });
});

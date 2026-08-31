import { execFileSync } from 'node:child_process';
import path from 'node:path';

// One question -- "which version is this?" -- used to have four answers, none of
// them the release the person downloaded. This module is the single source they
// all read from now: `git describe --tags --always`, turned into the two
// spellings its consumers need and nothing else.
//
//   stamp           what git said, unchanged. Goes into the Go ldflags, so
//                   `vc --version` inside the app equals `vc --version` from a
//                   release binary, and into the runtime manifest.
//   packageVersion  the same answer as semver, for electron-builder, which
//                   refuses a leading `v`.
//
// Nothing here invents a version. Every input git cannot have produced is
// refused, because a module that answered `0.1.0` on bad input would put the
// original lie back while keeping the build green.

const repo = path.resolve(import.meta.dirname, '../..');
// The electron-builder flag prefix that overrides a field of the packaged
// bundle's package.json. Named rather than spelled inline: this module composes
// the flag, it does not invoke the packer, and the two are checked apart.
const EXTRA_METADATA = '-c.extraMetadata';

/** The `-X` target: module path, package directory and exported variable. */
export const GO_VERSION_SYMBOL = 'github.com/makscee/void-code/internal/version.Version';

/** What electron-builder will accept: semver with an optional prerelease. */
export const PACKAGE_VERSION = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/;

// `git describe --tags` on or after a tag: v0.2.50, v0.2.50-3-gabc1234,
// v1.0.0-rc.1. The leading `v` is optional because a repository may tag without
// one; it is stripped for the semver spelling and kept for the stamp.
const DESCRIBED_TAG = /^v?(\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)$/;
// `--always` with no tag in reach: a bare abbreviated commit.
const DESCRIBED_COMMIT = /^[0-9a-f]{7,40}$/;

/**
 * Turn one `git describe` answer into the spellings its consumers need.
 *
 * @param {string} described exactly what git printed.
 * @returns {{ described: string, stamp: string, packageVersion: string }}
 */
export function parseBuildVersion(described) {
  if (typeof described !== 'string') throw new Error(`build version must be a string, not ${typeof described}`);
  const trimmed = described.trim();
  if (trimmed === '') throw new Error('build version is empty');
  const tagged = DESCRIBED_TAG.exec(trimmed);
  if (tagged !== null) return { described: trimmed, stamp: trimmed, packageVersion: tagged[1] };
  // A bare commit sorts below every real release as 0.0.0 and keeps the commit
  // in the prerelease, so a support report still identifies the build.
  if (DESCRIBED_COMMIT.test(trimmed)) return { described: trimmed, stamp: trimmed, packageVersion: `0.0.0-g${trimmed}` };
  throw new Error(`${JSON.stringify(described)} is not a version git describe can produce`);
}

/**
 * Ask git which version this working tree is, and refuse to guess if it cannot say.
 *
 * @param {(args: string[]) => string} [runGit] the command runner, for tests.
 */
export function readBuildVersion(runGit = (args) => execFileSync('git', args, { cwd: repo, encoding: 'utf8' })) {
  return parseBuildVersion(runGit(['describe', '--tags', '--always']));
}

function assertParsed(version) {
  if (typeof version !== 'object' || version === null || Array.isArray(version)) throw new Error('build version must be the object parseBuildVersion returns');
  const reparsed = parseBuildVersion(version.described);
  if (reparsed.stamp !== version.stamp || reparsed.packageVersion !== version.packageVersion) throw new Error('build version was altered after parsing');
  return version;
}

/**
 * The `go build` argv for the vc that ships inside the desktop bundle.
 *
 * -trimpath and -buildvcs=false are not decoration: the first keeps this
 * machine's paths out of the shipped binary, the second keeps a dirty worktree
 * from changing its bytes. The stamp is added to them, never instead of them.
 *
 * @param {string} destination where the binary is written.
 * @param {{ described: string, stamp: string, packageVersion: string }} version
 */
export function vcBuildArgs(destination, version) {
  assertParsed(version);
  return ['build', '-trimpath', '-buildvcs=false', '-ldflags', `-X ${GO_VERSION_SYMBOL}=${version.stamp}`, '-o', destination, './cmd/vc'];
}

/**
 * How the packer is told the version: on the command line, never by rewriting
 * the tree. A local build must not leave a modified desktop/package.json behind.
 *
 * @param {{ described: string, stamp: string, packageVersion: string }} version
 */
export function electronBuilderVersionArgs(version) {
  assertParsed(version);
  return [`${EXTRA_METADATA}.version=${version.packageVersion}`];
}

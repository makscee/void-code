// "Which version have you got?" is the first question asked of anyone with a
// problem, and the app could not answer it: the version appeared nowhere on
// screen, only inside the Support Report, behind a button, in a JSON blob you
// ask for after you already know what you are looking at.
//
// The decision about what that label says lives here rather than in index.ts
// because there is no DOM environment in this project's tests, and the failure
// being guarded is a label that renders as empty space -- an app that looks
// like it never had a version, indistinguishable from one whose version could
// not be read.

// What app.getVersion() returns for a stamped build: the semver spelling
// scripts/build-version.mjs produces, prerelease and all.
const PACKAGE_VERSION = /^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/;

/**
 * The text shown beside the product name. Never blank, for any input at all: a
 * bridge that answered nothing, a build that was not stamped, or a value of the
 * wrong type all say so out loud instead of disappearing.
 */
export function appVersionLabel(version: unknown): string {
  if (typeof version !== 'string') return 'version unknown';
  const trimmed = version.trim().replace(/^v/, '');
  // A build off the tag keeps its suffix: it must not be able to pass itself
  // off as the release it came after.
  return PACKAGE_VERSION.test(trimmed) ? `v${trimmed}` : 'version unknown';
}

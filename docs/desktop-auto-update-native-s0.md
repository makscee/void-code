# S0 — disposable native replacement / recovery experiment

09.09.2026. Continuation of the user-approved desktop auto-update spec, not a new
publication gate. Parent core: `c20ef9f`, PR #60; both push and PR CI succeeded
(`gh pr view 60 --repo makscee/void-code --json statusCheckRollup`). Work is isolated
on `work/desktop-update-native`; the reviewed core branch remains unchanged.

## Goal and boundaries

Establish whether a small external Go helper can replace an unsigned writable Mac
bundle and drive/recover an actual assisted per-user NSIS install on Windows.
This is S0, NOT a production helper or full Void Code R11 upgrade qualification.
Do not wire updater core/startup/IPC, enroll signing keys, change release workflows,
publish binaries/tags, touch installed applications, shared Windows seat or user GUI.
Use local macOS arm64 and disposable Windows GitHub runner. No visible windows/Dock
activation, auth/network use from fixture apps or modification of OS security policy.
Public dependency downloads during fixture packaging are allowed; update execution
itself is offline. All installer invocations are silent and fixture-only.

The test author owns the independent oracle INCLUDING the minimal packaged Electron
app's bootstrap receipt, resource marker and packaging fixtures. Letting the helper
implementer generate its own success witness would reproduce the mock-only proof bug.
The implementation author owns only the experiment helper and necessary CI wiring,
never test fixtures. Coordinator writes neither. Fixture infrastructure, RED tests,
and implementation are separate commits.

## Native acceptance

- Build real Electron app versions 1.0.0 and 1.0.1 using pinned electron-builder.
  Fixture appId/product/executable/registry GUID are unique, not Void Code's identity.
  Marker resources differ; app reads actual `app.getVersion()`, process architecture,
  installed resource bytes and executable path after `app.whenReady()`. Never accept
  a helper's own `ok` boolean as evidence that a packaged app booted.
- Explicit private temporary root and marker bind the request to disposable fixtures.
  HOME/userData/staging/receipts are isolated. Independent token/session/settings
  sentinels and an unrelated process must be unchanged. Reject symlink/junction target
  escape and mismatched fixture identity before mutation; never accept production ID.
- Independently start N and observe its receipt. Installation cannot mutate the target
  while N remains running. This probe may REFUSE a live predecessor; full R8's graceful
  shutdown/readiness protocol is still a later gate. No kill-by-name/PID guessing.
- Mac: full same-filesystem bundle replacement; retain complete old bundle through
  verified new bootstrap. Real framework symlinks and executable bits survive. Kill
  the real helper at durable boundaries before mutation, between the two renames,
  and after replacement before valid receipt; restart recovery and require intact
  runnable N, not just a journal claiming rollback.
- Windows: actual NSIS, exact Unicode/path-with-spaces target, `/currentuser`, no
  elevation/shortcuts/auto-launch. Provisional recovery candidate: retained immutable
  predecessor installer rerun into the same exact target. Qualify this experimentally,
  do not call it working beforehand. Assert both N bootstrap and own HKCU registration
  after recovery. Directory-only copy is NOT assumed to restore NSIS registration.
- A-owned NSIS fault fixture must mutate an actual install and terminate nonzero after
  a durable external witness (e.g. customInstall fault after ordinary extraction/registry
  work, removing a required resource). Recovery must restore N and its registry state.
  This proves partial transaction recovery; it is not every extraction failure class.
- Exit 0/spawn without valid bootstrap, missing/wrong resource marker, stale/wrong
  transaction receipt cannot commit N+1 or discard predecessor. One target has one
  live transaction; duplicate/recovery invocation cannot create duplicate bootstrap.
- Errors/recovery are bounded, old runnable version remains available; do not claim
  crash safety from catches alone. Use actual subprocess death at explicit fixture-only
  barriers. Native watchdogs bound tests; deterministic controller timing stays fake.

S0 does not implement trust/downloader/extractor/durable floor (R3/R6/R7), full R8
process/identity/recovery protocol, R9 UI, R10 publication or R11 full product migration.
The experiment accepts only its test-owned inputs, never an arbitrary installed app.
Gatekeeper/SmartScreen UX and quarantined internet delivery are not proved by a local
non-quarantined fixture; never remove quarantine or request real security dialogs.

## Source checks behind the experiment

Commands used against pinned app-builder-lib templates under `desktop/node_modules/`:
`sed -n '52,89p' app-builder-lib/templates/nsis/installSection.nsh` shows old uninstall
BEFORE extraction, registry writes BEFORE customInstall. `multiUser.nsh:102–130`
manually takes everything after `/D=`; it must be last/unquoted, including spaces.
`include/installer.nsh:103–151` writes own install path/version/uninstall registration;
`uninstaller.nsh:164–185,247–254` removes old install and registration. Thus install exit
alone and directory-only backup do not establish recovery.

Sparkle remains an alternative, not rejected as impossible without Developer ID:
https://sparkle-project.org/documentation/#3-segue-for-security-concerns says Developer
ID/notarization **if possible**, and separately recommends Ed25519 archive signatures.
Verified primary page 09.09; the search summary's stronger blanket requirement was NOT
adopted. Its programmatic Cocoa/framework integration is a separate cost, while Go can
exercise both current packaging formats without a new updater framework. S0 success,
not this preference, decides whether to continue with the helper candidate.

NSIS command-line docs: https://nsis.sourceforge.io/Docs/Chapter3.html (search result;
full fetch was blocked, so pinned template code above is the primary path evidence).
Apple's updating-Mac-software URL returned generic navigation on this fetch; no new
native behavior claim is based on that response.

## Next

Independent A refines the acceptance/API table from this document, then authors only
fixture infrastructure, then RED tests. B receives committed tests and cannot modify
them. Real native qualification, selected behavior-changing mutations, preflight and
frozen-diff panel follow. Failure on either OS keeps S0 open; portable unit-test success
cannot silently substitute for a skipped native case.

# Void Code 0.1.2 — signed one-click Windows acceptance runbook

> **Stable-track reference only.** This is not the current package identity or acceptance evidence. The current source package is `0.1.3-beta.5` on the separately signed closed-beta manifest path; do not apply this blocked stable-track procedure to it.

**Audience:** Maks and the Windows acceptance operator. **Target:** Windows 10/11 x64, per-user install. This is an operator procedure, not a claim that acceptance ran.

## Gate and evidence rules

Run this only with two frozen, immutable, Authenticode-signed builds from the same approved publisher: an older separately signed build and the new signed `Void-Code-0.1.2-windows-x64.exe`. No publisher is pinned in source: electron-builder must derive the single `app-update.yml` publisher from the actual approved certificate, while the package operator independently supplies its non-secret expected signer common name as `VC_DESKTOP_EXPECTED_PUBLISHER`. Package qualification requires Authenticode `Valid` and an exact certificate `SimpleName` match for both the frozen installer and packaged application executable before comparing generated update metadata. Missing expectation, certificate, or signature fails closed. The accepted stable manifest, `latest.yml`, installer hashes/sizes, and certificate-derived `app-update.yml` publisher must identify the new frozen build exactly. Verify each installer with `Get-FileHash -Algorithm SHA256` and `Get-AuthenticodeSignature`; require `Valid` and the exact approved publisher before execution.

Frozen 0.1.1 is `RETIRED_INTERNAL_REVIEW_FAILED`: it is unsigned, contains two S1 findings, and must never be published or accepted. Only 0.1.2 may become the post-fix candidate.

The current unsigned 0.1.2 build is only a **blocked candidate** pending signing. It cannot satisfy this runbook. Do not accept SmartScreen bypass, **Run anyway**, unsigned release behavior, disabled signature checks, portal mutation, or a substituted local feed. Signing authority and frozen signed artifacts are external gates.

Record only checklist label, UTC time, PASS/STOP, approved installer/manifest hashes, app version/locale, and candidate-owned process name/PID/parent PID/creation time. Never retain a one-time code or token, terminal/chat/prompt text, file contents, environment, command lines, raw errors/stacks, username/home path, unredacted workspace path, signed redirect query, or credentials.

Any STOP ends the run. Do not publish or mutate the production portal while running this procedure.

## 1. Clean-machine preflight

1. Use a clean Windows 10/11 x64 accountant profile with no Void Code user data and no running candidate-owned processes.
2. Confirm both frozen signed installers, their immutable evidence, and the accepted 0.1.2 manifest are present.
3. Verify SHA-256, Authenticode `Valid`, and exact approved publisher for both installers.
4. Confirm a disposable local workspace under `Documents\Void Code\<pilot task>`. Never use a live accounting database, whole client archive, network share, removable disk, cloud-sync root, credentials, or the only copy of any file.
5. Start network observation scoped to Void Code. It must be sufficient to prove updater hosts and that no locale bundle/language-pack request occurs, without retaining signed queries, credentials, or response bodies.

Record `SIGNED_PREFLIGHT PASS` or STOP.

## 2. Clean Russian default and authentication

1. Install and launch the new signed 0.1.2 build on the clean profile.
2. Before changing a setting, confirm sign-in/onboarding, Settings/About, updater labels/actions, support/recovery surfaces reached during the run, and release/version labels are Russian. Confirm About reports `0.1.2`.
3. Confirm no Marketplace, language-pack, locale JSON, or other locale network fetch occurred; both `en` and `ru` resources must be local packaged assets.
4. Select **Sign In**, complete browser authorization, select only the disposable workspace, and run one benign value-free chat. Record only readiness, never the code/token or content.

Record `CLEAN_RUSSIAN PASS`, `NO_LOCALE_FETCH PASS`, and `AUTH_APP_STATE_BASELINE PASS`, or STOP.

## 3. Explicit locale persistence

1. In Settings/About select **English**. Quit normally and launch from Start. Confirm the complete VC-owned surface is English and About remains 0.1.2. Record `ENGLISH_RESTART PASS`.
2. Select **Russian**. Quit normally and launch from Start. Confirm Russian. Record `RUSSIAN_RESTART PASS`.
3. Leave an explicit locale selected for the update scenario and record only `en` or `ru`. Do not delete locale/user data.

STOP on mixed language state, unhandled/persistence error, lost selection, or any locale network request.

## 4. Prepare the older separately signed build

1. Quit Void Code and prove candidate-owned processes have exited.
2. Preserve Electron workspace metadata, Pi sessions, and `%USERPROFILE%\.void-code\token`; do not inspect or delete them.
3. Install the approved older separately signed build using its normal per-user signed installer. Launch it and confirm its older version, exact publisher lineage, authentication readiness, disposable workspace/app state, and the explicit locale selected in §3.
4. Confirm the older build's updater initially shows a bounded current-version state.

Record `OLD_SIGNED_BUILD_READY PASS` or STOP.

## 5. One-click update to frozen 0.1.2

1. Trigger **Check for updates** if the automatic check has not completed. Confirm the accepted newer `0.1.2` is shown and **Update now** is available.
2. Select **Update now** exactly once. No browser, Downloads folder, external GitHub page, shell, file picker, or manually launched installer is allowed.
3. Observe in order: downloading with finite progress, verifying, installing/restarting. A standard OS elevation prompt is acceptable only if genuinely unavoidable and names the exact signed product/publisher; unexpected elevation is STOP.
4. Confirm Void Code restarts automatically into `0.1.2` without operator launching an installer.
5. Confirm the explicit locale from §3 is preserved, About reports 0.1.2, authentication remains ready, the disposable workspace/app state remains present, and a benign chat can still run.
6. Confirm observed updater traffic was limited to the canonical first-party manifest/feed/artifact, the exact immutable GitHub release URL, and its single observed GitHub release-asset redirect. Do not retain signed redirect queries.

Record `ONE_CLICK_PROGRESS_VERIFY_RESTART PASS`, `LOCALE_UPDATE_PRESERVED PASS`, and `AUTH_APP_STATE_UPDATE_PRESERVED PASS`, or STOP. A retryable failure may demonstrate safe failure behavior but does not pass the successful-update criterion.

## 6. Windows reboot persistence

1. Quit normally and run the survivor check in §7.
2. Reboot Windows normally; do not reset the profile.
3. Launch Void Code from Start. Confirm version 0.1.2, the explicit locale, authentication readiness, disposable workspace/app state, and no locale network fetch.

Record `WINDOWS_REBOOT_LOCALE PASS` and `WINDOWS_REBOOT_STATE PASS`, or STOP.

## 7. Updater, installer, temporary-file, and process survivors

At each boundary—before the old build, after automatic update restart, after normal quit, and after Windows reboot—inventory only candidate-owned processes by name/PID/parent PID/creation time. Do not enable a Command Line column and do not kill by process name. Ownership must be established from the recorded root and descendant relationships.

After update/restart, no updater or installer process may survive completion. After normal quit and reboot validation, no candidate-owned Void Code, Electron, private VC/Node/Pi, updater, installer, `OpenConsole`, or `conhost` descendant may survive. Inspect only the approved updater temporary/cache locations and assert there are no partial installer, pending download, or VC-owned temporary survivors; do not collect filenames containing signed queries or unrelated user files.

Record `NO_UPDATER_INSTALLER_TEMP_SURVIVORS PASS` and `NO_PROCESS_SURVIVORS PASS`, or STOP.

## 8. Final disposition

Acceptance requires every PASS above. Record one of:

- `SIGNED_ONE_CLICK_LOCALIZATION_ACCEPTANCE PASS — 0.1.2 <installer-sha256>`; or
- `SIGNED_ONE_CLICK_LOCALIZATION_ACCEPTANCE STOP — <checklist/coarse code>`.

This runbook and source/unit/package checks are not installed E2E evidence. Until signing authority supplies both frozen signed builds and this run completes, the current unsigned build remains blocked and production publication/portal mutation remains closed.

## Candidate tooling note

`npm run candidate:generate` and `npm run candidate:check` remain build-operator tooling only. Do not use them during this runbook to mint or alter a candidate; the signed frozen identities must already exist.

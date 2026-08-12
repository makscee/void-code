# Void Code 0.1.0 — guided Windows accountant pilot runbook

**Audience:** Maks (operator) and the first accountant, together at one Windows 10/11 x64 machine.  
**Scope:** one guided unsigned pilot install. This is not a public release.

## Evidence rule

Record only checklist item, UTC time, `PASS`/`STOP`, installer/report SHA-256, coarse recovery code, and process **name/PID/parent PID/creation time** needed below. Creation time is a value-free stable process identity used only to distinguish PID reuse. Never retain the one-time code or token, terminal/chat/prompt text, file contents, environment, command lines, raw errors/stacks, username/home path, client name, or unredacted workspace path. Do not screenshot PowerShell, terminal panes, folder paths, chats, SmartScreen details beyond the expected generic unsigned flow, or client files.

Any `STOP` ends the procedure. Do not improvise, disable antivirus, change auth, or select valuable data.

## 1. Machine and operator preflight

Maks verifies locally, recording only PASS/STOP:

- Windows 10/11 x64; ordinary accountant user can install per-user software.
- At least 2 GB free disk; current date/time; supported network available.
- No other Void Code, Pi, or session-maintenance process is running. Existing unrelated `node`, `vc`, `conhost`, or `OpenConsole` processes are noted by PID and left untouched.
- Exact candidate manifest, exact installer named `Void-Code-0.1.0-windows-x64.exe`, and retained predecessor installer are present from the controlled handoff.
- Candidate manifest checker passed on the build machine. The manifest says `product.name = Void Code`, `product.version = 0.1.0`, `signing.status = unsigned`, and carries the intended predecessor hash/reference and current operator-gate status. A `verified` value is only the manifest-declared status; it is not evidence that this machine's manual gate or the guided pilot passed.

**STOP:** wrong Windows/architecture, insufficient space, missing manifest/installer/predecessor, or unexpected concurrent Void Code, Pi, or session-maintenance process.

Pilot trust boundary: hostile mutation of the Pi session store by another process running as the same Windows user is outside the pilot integrity guarantee. Session discovery uses descriptor and pathname revalidation to reduce accidental replacement, but Pi still receives and reopens a pathname, so a residual pathname-handoff race remains. The no-concurrent-process gate and disposable copied-data rule are mandatory.

## 2. Existing authorized VC sign-in — no credential evidence

Maks obtains one authorized one-time code through the existing operator process. The accountant enters it directly; nobody says it aloud for recording, pastes it into chat, screenshots it, or writes it into evidence.

In PowerShell, use the existing VC installation and invoke the authorized flow without putting the literal code in shell history:

```powershell
vc login --code (Read-Host "Enter the one-time VC code")
```

After success, perform the value-free readiness check locally:

```powershell
vc status
```

Maks observes the result but records only `AUTH_READY PASS` or `AUTH_READY STOP`; do not copy its output. Close PowerShell. Never inspect `%USERPROFILE%\.void-code\token`.

**STOP:** login/readiness fails, asks for a materially different flow, or requires exposing a code/token. Do not modify production auth.

## 3. Verify SHA-256 before SmartScreen

Open a fresh PowerShell in the handoff folder and compute:

```powershell
$installer = Get-Item .\Void-Code-0.1.0-windows-x64.exe
$actual = (Get-FileHash $installer.FullName -Algorithm SHA256).Hash.ToLowerInvariant()
$actual
```

Compare `$actual` character-for-character with `installer.sha256` in the exact candidate manifest. Record only the basename, expected hash, actual hash, and PASS/STOP. Do this **before** opening the installer or bypassing SmartScreen.

**STOP immediately:** hash mismatch, wrong basename/version, missing manifest, zero/implausible file, or more than one candidate installer. Delete nothing and do not run it.

## 4. Verify unsigned identity and choose the MOTW branch

Keep `$installer` bound to the exact hash-verified file. Independently check its Authenticode status:

```powershell
$signatureStatus = (Get-AuthenticodeSignature -LiteralPath $installer.FullName).Status
if ($signatureStatus -ne [System.Management.Automation.SignatureStatus]::NotSigned) { throw "STOP" }
```

Record only `SIGNATURE_NOT_SIGNED PASS` or `SIGNATURE_NOT_SIGNED STOP`. Do not record signature status output, certificate or publisher details. A result other than exactly `NotSigned`, a query failure, or a result for a different basename is **STOP**. This check supplements and never substitutes for the manifest hash comparison.

Next inspect only whether that same file has a `Zone.Identifier` alternate data stream. Do not read or record stream contents, URL, host, referrer, zone value, paths, screenshots, shell output, or signature details.

```powershell
$motw = Get-Item -LiteralPath $installer.FullName -Stream Zone.Identifier -ErrorAction SilentlyContinue
```

An inspection failure, ambiguous result, or inspection of a different file is **STOP**. Record exactly one branch label:

### `MOTW_ABSENT PASS`

No `Zone.Identifier` stream exists. This is accepted for a controlled handoff or local copy. Double-click the exact verified installer and expect direct launch with no SmartScreen or other execution/security prompt. Do not solicit or claim a SmartScreen dialog. Complete the per-user installer for **Void Code**. If any execution/security prompt appears, **STOP**; do not bypass it.

### `MOTW_PRESENT PASS`

A `Zone.Identifier` stream exists. Double-click the exact verified installer. The only accepted generic unsigned SmartScreen path is:

1. **Windows protected your PC** / unknown publisher warning.
2. Select **More info**.
3. Confirm the app is the exact already-verified `Void-Code-0.1.0-windows-x64.exe`.
4. Select **Run anyway**.
5. Complete the per-user installer with product name **Void Code**.

Record only `SMARTSCREEN_UNSIGNED PASS`, not dialog details.

For either branch, do not change the install directory unless the documented default is unavailable. **STOP:** the hash changes; Authenticode is not exactly `NotSigned`; MOTW inspection fails or is ambiguous; a named or unexpected publisher appears; Windows requests disabling security; the prompt names another product/version/file; elevation or an admin/system-wide change is unexpectedly required; any prompt differs materially; or the absent-MOTW branch shows a prompt. Do not “try anyway.”

## 5. Safe first workspace

In File Explorer create one dedicated local folder:

```text
Documents\Void Code\<pilot task>
```

Use a neutral task label in evidence; never record the actual path/client name. Put only disposable samples or **copies** of a few non-sensitive files into it.

Never use for first run:

- a live accounting database or its active data directory;
- a whole client archive;
- a network share, mapped drive, removable disk, or cloud-sync root (OneDrive/Dropbox/etc.);
- credentials, exports containing secrets, or the only copy of any file.

The accountant must acknowledge: **Pi can read and change everything inside the selected folder with the accountant's Windows permissions.** Keep originals outside it and backed up.

**STOP:** only live/valuable data is available, the folder is synced/network/removable, or the trust statement is not understood.

## 6. Exact product walkthrough

Retain no chat text, terminal content, file content, client/path screenshot, or session identifier. Record only each numbered result.

1. Launch **Void Code** from Start without PowerShell or a terminal. Expect name `Void Code` and version `0.1.0` where shown by Windows metadata.
2. Choose only the prepared local folder and read the trusted-folder disclosure.
3. Select **New Chat**. Send one benign value-free request about a disposable sample, such as asking Pi to list file **types/counts without printing names or contents**. Record `FIRST_CHAT PASS` only.
4. Select **New Chat** again. Confirm two tabs, shared-folder warning, independent interaction, and background `Working` → `Ready`/unread. Record `TWO_CHAT_STATUS PASS` only.
5. Close the second chat to Recent, then Resume it. Confirm the same conversation returns without recording its ID/content. Record `CLOSE_RESUME PASS`.
6. Quit Void Code. Run the after-quit inventory in §8. Relaunch from Start and confirm lazy restoration. Record `QUIT_RELAUNCH PASS`.
7. Missing-folder recovery using only this disposable folder: quit; rename the folder in File Explorer; relaunch; expect **Workspace unavailable** with Locate/Remove and no raw path/fallback directory. Choose Locate and select the renamed folder; verify recovery. Record `MISSING_FOLDER WORKSPACE_MISSING PASS`. Do not delete it during the test.
8. Open **Support**. Confirm the notice says secrets/content/paths are excluded. Use **Copy Report**, inspect locally that only the documented allowlist is present, then **Save Report**. Record the report SHA-256 and coarse code only; retain the report only in the controlled support handoff. Record `SUPPORT_REPORT PASS`.

**STOP:** shell fallback, wrong folder, content/path/secret in evidence or Support Report, lost chat, unexpected deletion, misleading status, recovery outside the selected folder, or any raw error/path shown in recovery.

## 7. Failure and support handling

Use only the on-screen action and VC-25 coarse code:

| Code | Guided action |
|---|---|
| `AUTH_PREFLIGHT_REQUIRED` | Maks repeats the value-free `vc status` observation; if missing, repeat authorized login outside Void Code. |
| `SESSION_START_FAILED` | Verify existing sign-in and network, then Restart once; if repeated, Save Support Report and STOP. |
| `RUNTIME_EXITED` | Restart once; if repeated, verify sign-in/network, Save Support Report and STOP. |
| `WORKSPACE_MISSING` | Locate the moved disposable folder or Remove the saved workspace; never select a fallback/valuable folder. |
| `SESSION_MISSING` | Close that saved chat and start a new one; do not delete other chats/files. |

Support evidence may contain only the allowlisted report plus its SHA-256 and checklist results. Never collect token/code, env, terminal output, prompt/chat, file content, command line, stack, username/home path, session ID, or raw workspace/client path. Do not create ad-hoc logs.

## 8. Narrow Windows process inventory

Do not enable a Command Line column, run `wmic ... commandline`, capture environment, or export the full process table.

### Before launch

In Task Manager → **Details**, record only name and PID for existing processes named `Void Code`, `vc`, `node`, `OpenConsole`, or `conhost`. This is the baseline; unrelated processes must remain untouched. Expect no `Void Code` process.

### During launch

Record the candidate `Void Code` root PID from Task Manager. In PowerShell, enter that PID when prompted; this script reads only name/PID/parent PID/creation time and emits only the candidate descendant tree. Creation time is retained in approved process rows so exit checks compare `(PID, creation time)` rather than treating a reused PID as candidate-owned:

```powershell
$rootPid = [int](Read-Host "Void Code root PID")
$rows = Get-CimInstance Win32_Process | Select-Object Name,ProcessId,ParentProcessId,CreationDate
$ids = [System.Collections.Generic.HashSet[int]]::new()
[void]$ids.Add($rootPid)
do {
  $added = $false
  foreach ($p in $rows) {
    if ($ids.Contains([int]$p.ParentProcessId) -and $ids.Add([int]$p.ProcessId)) { $added = $true }
  }
} while ($added)
$rows | Where-Object { $ids.Contains([int]$_.ProcessId) } | Sort-Object ProcessId | Format-Table Name,ProcessId,ParentProcessId,CreationDate
```

Confirm the tree is bounded to candidate-owned Electron/Void Code, private `vc`, Node/Pi and Windows ConPTY host descendants. Record only those four columns. The rehearsal tool validates prior evidence's exact schema, phase, result/code, real UTC timestamp, root identity and descendant relationships, then compares both PID and creation time. A PID occupied by a process with a different creation time is reused and is not reported as a surviving candidate process.

### After chat close and app quit

Close a chat and confirm its previously recorded owned descendants leave. Quit Void Code normally, wait 10 seconds, then use Task Manager names/PIDs and the same filtered tree method. All candidate PIDs must be gone, including Electron/Void Code, private `vc`, Node/Pi, `OpenConsole`/`conhost` descendants. Baseline unrelated PIDs remain.

**STOP:** any candidate descendant survives normal quit, ownership is ambiguous, or proving ownership would require command-line/secret capture. Do not kill by process name; record STOP for repair.

## 9. Uninstall, persistence, deletion and rollback

### Normal uninstall

Windows **Settings → Apps → Installed apps → Void Code → Uninstall**. Confirm app/shortcuts are removed. Repeat §8 after-uninstall inventory.

Uninstalling the app does **not** imply deletion of:

- Electron workspace metadata under the user's application-data area;
- Pi chat/session data under `%USERPROFILE%\.pi\agent\sessions`;
- the VC token under `%USERPROFILE%\.void-code\token`.

Do not open or capture these files. Do not delete any of them during normal uninstall/rollback.

### Explicit deletion policy

Token deletion is a separate operator-approved `vc logout`. Chat/session or Electron metadata deletion requires separate written approval, a stated backup/retention decision, and a dedicated procedure. Never bundle those deletions into uninstall or rollback.

### Rollback

1. Verify the retained predecessor installer's SHA-256 against `predecessor.installerSha256` in the candidate manifest; confirm its immutable `predecessor.reference`.
2. **STOP** on missing predecessor, mismatch, mutable label such as `latest`, or unexpected prompt.
3. Uninstall the current Void Code app through Installed apps. Preserve token/chats/metadata.
4. Install the retained prior installer using its documented unsigned flow only after hash verification.
5. Recompute and compare its SHA-256 after handoff, launch, and repeat the minimal readiness/process checks. Record `ROLLBACK PASS` or `STOP` without content/paths.

## 10. Final disposition

Maks records one of:

- `GUIDED_PILOT PASS — candidate hash <sha256>` only after every applicable step and the real Windows gate pass; or
- `GUIDED_PILOT STOP — <checklist item/coarse code>`.

A package fixture or this document alone is never Windows operator acceptance.

## Candidate manifest tooling (build operator, not accountant)

Run only **after** the tooling/runbook commit is on clean synchronized `main`, and only after building the exact Windows installer and private-runtime `manifest.json`. Do not use these commands during this tooling slice to create a real candidate.

From `desktop/`:

```bash
npm run candidate:generate -- \
  --installer release/Void-Code-0.1.0-windows-x64.exe \
  --resources resources/staged/manifest.json \
  --arch x64 \
  --build-timestamp 2026-07-27T12:34:56.000Z \
  --predecessor-ref VC14-prototype-80487da8 \
  --predecessor-sha256 <64-lowercase-hex> \
  --operator-gate blocked \
  --gate-evidence VC-19 \
  --output release/Void-Code-0.1.0-windows-x64.candidate.json

npm run candidate:check -- \
  --manifest release/Void-Code-0.1.0-windows-x64.candidate.json \
  --installer release/Void-Code-0.1.0-windows-x64.exe \
  --resources resources/staged/manifest.json
```

Use the actual canonical UTC build time, not the example. `verified` operator status additionally requires `--gate-verified-at <canonical-UTC-ISO>` and immutable evidence reference. Generator/checker fail on dirty/diverged/unresolved source, existing output, wrong identity/name/platform, missing/symlink/empty files, changed hashes/sizes, malformed timestamp/hash/reference, mutable predecessor label, non-unsigned status, or inconsistent gate fields.

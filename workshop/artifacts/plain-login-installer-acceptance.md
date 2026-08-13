# Canonical installer/login contract — implementation evidence

Commit: `c3b7bae` (follow-up evidence commit recorded in repository history)

## Scope and acceptance trace

- **criterion-1:** Removed obsolete `VC_CODE` and `vc login --code` installer branches and guidance from both installers, README, macOS/Windows setup docs, and the affected desktop pilot runbook; retained explicit plain interactive `vc login` instructions. Added focused static installer contract tests plus dry-run non-writing checks. No auth runtime, production, release, tag, push, Windows host, or secrets work was performed.
- **criterion-2:** Evidence below records changed paths, tests, actual command results, unavailable checks, residual risk, commit, and repository cleanliness.

## Validation evidence

- Static Python assertions passed for `install.sh`, `install.ps1`, `README.md`, `docs/mac-setup.md`, `docs/windows-setup.md`, and `desktop/docs/windows-accountant-pilot-runbook.md`: obsolete forms absent and plain `vc login` present.
- `sh -n install.sh`: passed.
- `git diff --check`: passed before commit.
- Shell dry-run with an empty temporary HOME and unreachable local auth host: exited 0, printed `NEXT: vc login`, and left temporary HOME empty (`before=''`, `after=''`).
- `go test ./...`: not run because no `go` or `gofmt` executable is installed; `.go-version` requests 1.26.5.
- PowerShell syntax/runtime dry-run: not run because neither `pwsh` nor `powershell` is installed. The Go static test asserts its dry-run guard precedes the first filesystem write and contains `exit 0`; when PowerShell exists it also executes the dry-run against an empty temporary `USERPROFILE`.

## Residual risks

- The newly added Go tests could not execute in this environment due to the absent Go toolchain.
- PowerShell parsing and live dry-run execution could not be exercised here due to the absent PowerShell runtime.
- Reviewer gate remains required by the acceptance contract.

```acceptance-report
{
  "criteriaSatisfied": [
    {
      "id": "criterion-1",
      "status": "satisfied",
      "evidence": "Installers/docs now use plain interactive vc login; installer_contract_test.go statically rejects VC_CODE and login --code and checks dry-run write boundaries."
    },
    {
      "id": "criterion-2",
      "status": "satisfied",
      "evidence": "This artifact records changed files, tests, commands, actual validation output, residual risks, commit state, and no staged files."
    }
  ],
  "changedFiles": [
    "README.md",
    "desktop/docs/windows-accountant-pilot-runbook.md",
    "desktop/tests/pilot-runbook.test.ts",
    "docs/mac-setup.md",
    "docs/windows-setup.md",
    "install.ps1",
    "install.sh",
    "installer_contract_test.go",
    "workshop/artifacts/plain-login-installer-acceptance.md"
  ],
  "testsAddedOrUpdated": [
    "installer_contract_test.go",
    "desktop/tests/pilot-runbook.test.ts"
  ],
  "commandsRun": [
    {
      "command": "python3 static assertions over installers and setup documentation",
      "result": "passed",
      "summary": "Both installers and named docs contain no VC_CODE/login --code and do contain vc login."
    },
    {
      "command": "HOME=<empty-temp> VC_AUTH_HOST=http://127.0.0.1:1 sh install.sh --dry-run",
      "result": "passed",
      "summary": "Exited 0, printed NEXT: vc login, and temporary HOME remained empty."
    },
    {
      "command": "sh -n install.sh",
      "result": "passed",
      "summary": "POSIX shell syntax accepted."
    },
    {
      "command": "git diff --check",
      "result": "passed",
      "summary": "No whitespace errors."
    },
    {
      "command": "CGO_ENABLED=0 go test ./...",
      "result": "not-run",
      "summary": "Go executable unavailable; .go-version requests 1.26.5."
    },
    {
      "command": "PowerShell parse and dry-run",
      "result": "not-run",
      "summary": "Neither pwsh nor powershell is installed."
    },
    {
      "command": "git commit -m 'fix(installer): use interactive login contract'",
      "result": "passed",
      "summary": "Created commit c3b7bae."
    }
  ],
  "validationOutput": [
    "PASS install.sh: obsolete forms absent; plain login present",
    "PASS install.ps1: obsolete forms absent; plain login present",
    "Shell dry-run: rc=0, before='', after='', NEXT: vc login",
    "PASS sh -n install.sh",
    "PASS git diff --check"
  ],
  "residualRisks": [
    "Go tests were not executable because the Go toolchain is absent.",
    "PowerShell syntax/live dry-run was not executable because PowerShell is absent.",
    "Independent reviewer gate remains required."
  ],
  "noStagedFiles": true,
  "diffSummary": "Removed automatic access-code installer behavior and obsolete code-based guidance, documented plain interactive login, and added focused installer contract/dry-run tests.",
  "reviewFindings": [
    "no implementation blockers; reviewer gate pending"
  ],
  "manualNotes": "No tag, push, release, Windows host, production system, or secrets were used."
}
```

# Accepted reviewer findings — implementation evidence

## Outcome

Implemented the accepted findings without changing authentication runtime behavior:

- Removed automatic/access-code login guidance from the installers and named user documentation.
- Updated root command help to describe interactive/device authentication rather than access-code authentication.
- Extended the canonical stable release `publish-auth` job to check out void-code and copy `install.sh` and `install.ps1` to `void-auth/public/vc/`, while retaining binary download, version manifest generation, binary executable modes, and binary staging.
- Added static contract tests for docs, root help, and stable release publication; retained installer dry-run tests.

Commits in this work branch:

- `c3b7bae fix(installer): use interactive login contract`
- `7f9f85a docs: record installer acceptance evidence`
- `58497ca fix(release): publish canonical installers`

No tag, push, or release was performed.

## Changed files against origin/main

- `.github/workflows/release.yml`
- `README.md`
- `cmd/vc/root.go`
- `desktop/docs/windows-accountant-pilot-runbook.md`
- `desktop/tests/pilot-runbook.test.ts`
- `docs/mac-setup.md`
- `docs/windows-setup.md`
- `install.ps1`
- `install.sh`
- `installer_contract_test.go`
- `workshop/artifacts/plain-login-installer-acceptance.md`
- `workshop/artifacts/accepted-reviewer-findings.md`

## Tests added or updated

- `installer_contract_test.go`: installer login contract; user-doc obsolete wording guard; root-help contract; stable workflow installer/binary sync contract; shell and PowerShell dry-run guards.
- `desktop/tests/pilot-runbook.test.ts`: expected pilot authentication command updated to plain `vc login` by the preceding accepted-finding commit.

## Commands and validation output

1. `gofmt -w installer_contract_test.go && go test ./...` — **failed/unavailable**: `/bin/bash: line 1: gofmt: command not found` (exit 127). No Go installation is present in this environment; `.go-version` requests `1.26.5`.
2. `git diff --check && ruby .github/scripts/check-release-auth-commit-message.rb && ruby .github/scripts/check-release-tag-graphs.rb && sh -n install.sh` — **passed**. Output:
   - `auth-sync commit message has a recognized co-author trailer and no Ansible instructions`
   - `release tag graphs are exclusive: v1.2.3 => stable, v1.2.3-vi20.1 => canary`
3. Python static assertions over installers/docs/root help/release workflow — **passed**. Output:
   - `plain-login static assertions passed for 6 files`
   - `release sync and root-help static assertions passed`
4. `HOME=<empty-temp> VC_AUTH_HOST=http://127.0.0.1:1 sh install.sh --dry-run` plus empty-HOME assertion — **passed**. Signal output:
   - `NEXT: vc login`
   - `exit=0 home_entries=0`
5. `ruby -e 'require "yaml"; YAML.load_file(".github/workflows/release.yml"); puts "release workflow YAML parsed"' && ruby .github/scripts/check-release-auth-commit-message.rb && ruby .github/scripts/check-release-tag-graphs.rb` — **passed**. Output:
   - `release workflow YAML parsed`
   - both workflow script success messages above.
6. `git diff --check origin/main...HEAD && sh -n install.sh` — **passed**, no output.
7. `command -v pwsh || command -v powershell` — neither present, so executable PowerShell dry-run was unavailable; its ordering/content is covered statically.

## Acceptance trace

- **criterion-1 — satisfied:** focused diffs remove misleading login wording, correct `cmd/vc/root.go`, and add stable-only canonical installer copying alongside the unchanged binary route. Static assertions, YAML parsing, shell syntax, dry-run, and release graph checks passed.
- **criterion-2 — satisfied:** changed paths, tests, commands, actual outputs, commits, and residual risks are recorded here; final status check establishes no staged files.

## Review findings / residual risks

- No blocker found in the implementation diff.
- `go test ./...` could not run because Go/gofmt is absent.
- PowerShell executable validation could not run because pwsh/powershell is absent.
- GitHub Actions and cross-repository void-auth publication were not triggered, as required by the no-tag/no-push/no-release boundary.

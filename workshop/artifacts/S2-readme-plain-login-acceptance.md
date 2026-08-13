# S2 README plain-login implementation evidence

Outcome: README stale `vc login --device` usage was removed, and the existing user-doc static assertion was extended to reject `login --device` regressions. Commit `407d42c6ba5885e77719c99ec2ec378a92d04c6f` was pushed to `origin/work/VC-plain-login-installer`. No release was performed.

## Changed paths

- `README.md`
- `installer_contract_test.go`

## Tests added or adjusted

- Adjusted `TestUserDocsDoNotPromiseAutomaticOrAccessCodeLogin` to reject `login --device` in covered user docs, including `README.md`.

## Commands and output

- `go test ./...` — not run: the environment has no `go` or `gofmt` executable (`/bin/bash: gofmt: command not found`, exit 127).
- `git diff HEAD^ HEAD --check && ! git grep -F 'vc login --device' HEAD -- ':!workshop/artifacts/*'` — passed: `PASS: no tracked vc login --device references at HEAD`.
- `git commit -m "docs: use plain login in README"` — passed: commit `407d42c`, 2 files changed, 1 insertion, 2 deletions.
- `git push origin HEAD` — passed: `73eee36..407d42c HEAD -> work/VC-plain-login-installer`.
- `git ls-remote --heads origin work/VC-plain-login-installer` — passed: remote head `407d42c6ba5885e77719c99ec2ec378a92d04c6f`.
- `git status --short --branch` and `git diff --cached --name-only` — passed: branch aligned with origin and no staged files.

## Acceptance trace

- criterion-1: satisfied — the only product change removes the stale README command; the test-only change statically prevents its return. Commit stat shows only the two named files.
- criterion-2: satisfied — exact commit, remote ref, diff check, static grep, changed paths, and test-environment limitation are recorded here.

## Review envelope

Reviewer gate remains required. Self-review found no blocker: the assertion uses the existing table-driven docs contract and the README now presents only the supported plain login command. No release action was taken.

## Residual risks

- The Go suite could not run because the execution image lacks the Go toolchain. The changed Go source is a one-string addition to an existing composite literal and did not require formatting changes.

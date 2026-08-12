# VC-32 S1 repair evidence

## Outcome

Applied the accepted S1 repair at HEAD `0853043`: `launchDiagnostics.record` now drops valid once-only records that arrive after the terminal-safe flush instead of writing them immediately. The existing fixed schema, phase policy, once-only tracking, and ignored diagnostic write errors remain unchanged.

## Acceptance trace

| ID | Result | Exact evidence/check |
|---|---|---|
| criterion-1 | Scope-limited terminal-safety repair plus deterministic delayed provider/update regression test. No phase, schema, product policy, launch behavior, or Pi smoke code changed. | Diff is limited to `cmd/vc/launch_diagnostics.go` and `cmd/vc/launch_diagnostics_test.go`; focused race, full suite, Pi smoke classification, and static build all passed. |

## Changed files

- `cmd/vc/launch_diagnostics.go`
- `cmd/vc/launch_diagnostics_test.go`
- `workshop/artifacts/VC-32-S1-repair-evidence.md`

## Test added

- `TestLaunchDiagnostics_DelayedPreflightCompletionWritesNoPostFlushBytes`: gates provider and update completion on channels, flushes at spawn handoff, releases both delayed workers, waits for completion, and asserts output remains byte-identical with neither late phase included.

## Commands run

- `go test -race ./cmd/vc -run 'TestLaunchDiagnostics_' -count=10` — PASS: `ok github.com/makscee/void-code/cmd/vc 1.447s`.
- `CGO_ENABLED=0 go test ./...` — PASS: all packages passed; `cmd/vc 30.638s`.
- `CGO_ENABLED=0 go test ./cmd/vc -run 'TestPi.*Smoke|TestPiExtensionSmoke' -v -count=1` — PASS: `TestPiVoidDeepSeekExtensionSmoke` and `TestPiVoidCodexExtensionSmoke`; package completed in `12.062s`. The previously known Pi smoke baseline did not reproduce on this run; no Pi code was changed.
- `CGO_ENABLED=0 go build -ldflags "-X github.com/makscee/void-code/internal/version.Version=dev" ./cmd/vc` — PASS, no output.
- `git diff --check` — PASS, no output.

## Residual risks

- Records incomplete at the sole terminal-safe flush are intentionally absent from diagnostics. This is the accepted tradeoff required to prevent post-handoff terminal writes.
- Diagnostic output write failures remain intentionally ignored so diagnostics cannot alter launch.

## Review envelope

Review only the late-record branch in `launchDiagnostics.record` and the delayed preflight regression test. No unrelated smoke repair or product-policy change is included.

# VC console simplification deletion map

## Delete now

| Surface | Files/tests |
|---|---|
| Persisted user harness choice and Claude/Codex launch paths | `internal/harnesschoice/`, `internal/claudebin/`, `internal/codexbin/`, `internal/ccjson/`, `internal/ccsettings/`, `cmd/vc/permmode.go`, `cmd/vc/hook.go`, `cmd/vc/statusline.go` and their tests |
| Persisted provider/BYO-key choice, relay matrix and reconciliation | `internal/provider/`, `internal/compat/`, `internal/keystore/`, `internal/harness/direct/`, `internal/harness/relay/`, `internal/welcome/providers.go`, and their tests |
| Welcome provider/harness menus, status badges, install/menu dispatch and provider preflight | provider/harness portions of `internal/welcome/welcome.go`, `internal/welcome/harnesses.go`, `cmd/vc/main.go`, `cmd/vc/launch_preflight.go`, `cmd/vc/doctor.go`, `cmd/vc/status.go`, plus focused tests |
| VC-owned Pi default-model/provider injection and settings fallback | `cmd/vc/pi_settings.go`; `buildPiVoid*Args`, `--provider`/`--model` injection tests |
| Product copy describing a selectable harness/provider matrix | `README.md`, root command copy, doctor/status copy, and stale product docs |

## Retain as internal invariants

| Invariant | Files/call path |
|---|---|
| One Pi runtime executable is checked then launched through the `internal/harness.Spawn` seam | `internal/pibin/`, `internal/harness/`, `cmd/vc/main.go` |
| Subscription auth, budget admission, CA resolution and secret stripping | `internal/auth/`, `internal/config/`, `cmd/vc/main.go` |
| Managed Pi extension and `pi-bootstrap` authenticate transport and register subscription-granted models for Pi's **native** selector | `cmd/vc/pi_extension.go`, `cmd/vc/pi_managed.go`, `cmd/vc/pi_bootstrap.go` |
| Desktop private-Pi runtime hardening, update behavior and lifecycle channel | `cmd/vc/desktop_session.go` and desktop package; adapt only where it shares removed console choice state |

Legacy `active_harness`, `active_provider`, and `active_provider_label` config keys are ignored. They are neither read nor rewritten.

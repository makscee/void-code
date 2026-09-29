# void-code — agent context

## What this is

`vc` launches a coding-agent harness on a void-code subscription. Today the
only harness is Pi: vc installs a pinned Pi, signs in through Void Identity,
and hands Pi a transport extension that talks to the relay. Pi's own UI owns
model selection. More harnesses (a harness picker) are planned; see the vc
architecture map in `team-void-m/void-works-wiki`
(`wiki/concepts/vc-architecture-map.md`) for where each layer lives today.

Single static Go binary (`cmd/vc`), plus an Electron desktop app in `desktop/`
that wraps it.

## Module

`github.com/makscee/void-code` — single module, default branch `main`.

## Build

```bash
go build -ldflags "-X github.com/makscee/void-code/internal/version.Version=dev" ./cmd/vc
go test ./...
```

CGO_ENABLED=0 always — static binary, no libc dep.

## Frozen interface contracts (binding — do not renegotiate)

| Contract | Value |
|---|---|
| Binary | `vc` |
| Token file | `~/.void-code/token` mode 0600 |
| Cache dir | `~/.void-code/` |
| Pi runtime | `~/.void-code/runtime/pi` (never a `pi` from `PATH`) |
| Relay CA cache | `~/.void-code/relay-ca.pem` |
| Relay host default | `relay.makscee.ru:443` (https); `:8448` plaintext still supported via `VC_RELAY_HOST=http://relay.makscee.ru:8448` |
| Auth host default | `https://auth.makscee.ru` |
| Env override: relay | `VC_RELAY_HOST` |
| Env override: CA | `VC_RELAY_CA` |
| Env override: auth | `VC_AUTH_HOST` |
| Env override: access check | `VC_ACCESS_CHECK_HOST` (defaults to the relay) |
| GH artifacts | `vc-{darwin,linux,windows}-{amd64,arm64}` (windows: `.exe`) |
| Spawn seam | `internal/harness.Spawn(ctx, wrappedBin, args, env)` |

Pi's environment is built by `buildPiSpawnEnv` in `cmd/vc/main.go`. The Pi
extension gets its credentials from `vc pi-bootstrap`, not from the environment.

## Package layout

```
cmd/vc/         — main package: Cobra commands, Pi launch, Pi extension
                  (pi_extension.go), desktop-session
internal/
  auth/         — token store, device flow, /v1/vc/me, wallet, access requests
  browser/      — open URLs
  childenv/     — PATH and env for the Pi child process
  clackui/      — terminal UI pieces
  config/       — env resolution (VC_* vars), cache paths
  harness/      — Spawn seam (passthrough stdio); direct/ strips env
  pibin/        — resolve the managed Pi entrypoint
  piruntime/    — install the pinned Pi runtime
  provider/     — relay route kinds used by buildPiSpawnEnv
  releasesums/  — checksum-checked release downloads
  update/       — vc self-update
  version/      — build-time Version var
  welcome/      — landing screen
desktop/        — Electron + xterm desktop app
.github/workflows/ — release builds on tag push
```

## TDD

Every `internal/` package with behaviour has a `*_test.go`. bubbletea views use `teatest`.  
Run `go test ./...` before every commit.

## Written fresh

vc was written fresh, not imported from claudev. No `~/.claudev/token` compat,
no migration banner, no two-launch update, no `--bare` flag.

## Windows first-class

- Pi's npm `.cmd` shim is launched through `cmd.exe` by `harness.Spawn`
- Spawn uses `cmd.Run()` (not `syscall.Exec`) — ConPTY compatible
- TUI welcome exits before spawning Pi (never concurrent)
- Verify Win11 on tower:230 (`qm sendkey` + `screendump`) at milestone boundaries

## Release

Tag `v*.*.*` → CI builds 6 binaries + version.json → GH Release.  
`internal/version.Version` injected via `-ldflags`.

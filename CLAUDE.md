# void-code — agent context

## What this is

`vc` is a relay harness over `claude` (and later codex/pi). Single static Go binary.

Design canon: `hub/vault/projects/void-code/CONTEXT.md`  
ADR: `docs/adr/0002-void-code-fresh-harness.md`  
Task tree: `hub/vault/work/tasks/active/VCD-*.md`

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
| Relay CA cache | `~/.void-code/relay-ca.pem` |
| Relay host default | `relay.makscee.ru:8448` |
| Auth host default | `https://auth.makscee.ru` |
| Env override: relay | `VC_RELAY_HOST` |
| Env override: CA | `VC_RELAY_CA` |
| Env override: auth | `VC_AUTH_HOST` |
| Env into claude | `HTTPS_PROXY` + `NODE_EXTRA_CA_CERTS` + `CLAUDE_CODE_OAUTH_TOKEN` + `ANTHROPIC_API_KEY=` (empty) + `ANTHROPIC_BASE_URL=` (empty) |
| Env stripped | `CLAUDE_CODE_OAUTH_TOKEN`, `HTTPS_PROXY`, `NODE_EXTRA_CA_CERTS`, `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL` |
| GH artifacts | `vc-{darwin,linux,windows}-{amd64,arm64}` (windows: `.exe`) |
| Spawn seam | `internal/harness.Spawn(ctx, wrappedBin, args, env)` |

## Package layout

```
cmd/vc/         — main package: Cobra wiring, sub-command stubs
internal/
  config/       — env resolution (VC_* vars)
  harness/      — Spawn seam (passthrough stdio)
  version/      — build-time Version var
  auth/         — (VCD-3) token store + code-exchange + device flow
embed/          — relay-ca.pem (populated by VCD-4)
docs/adr/       — architecture decisions
.github/
  workflows/
    release.yml — 6-arch matrix on tag push
```

## TDD

Every `internal/` package has a `*_test.go`. bubbletea views use `teatest`.  
Run `go test ./...` before every commit.

## DO NOT inherit from claudev

Only these 5 patterns are re-implemented (not imported):
1. POST access code → token via `/v1/auth/access-codes/exchange`
2. Fetch + embed relay CA
3. Export `HTTPS_PROXY` + `NODE_EXTRA_CA_CERTS`
4. `Spawn(ctx, wrappedBin, args, env)` with passthrough stdio
5. Wipe `~/.void-code/token` on logout

Everything else is fresh. No `~/.claudev/token` compat, no migration banner,  
no two-launch update, no `--bare` flag.

## Windows first-class

- `claude.cmd` (npm shim) resolved via `exec.LookPath("claude")` — works on Windows
- Spawn uses `cmd.Run()` (not `syscall.Exec`) — ConPTY compatible
- TUI welcome exits before spawning claude (never concurrent)
- Verify Win11 on tower:230 (`qm sendkey` + `screendump`) at milestone boundaries

## Release

Tag `v*.*.*` → CI builds 6 binaries + version.json → GH Release.  
`internal/version.Version` injected via `-ldflags`.

# void-code

`vc` — relay harness for Claude Code and Pi. Routes your selected coding harness through void-relay for authentication and provider selection.

## Install

```bash
curl -fsSL https://auth.makscee.ru/vc/install.sh | VC_CODE=ABCD-EFGH sh
```

Windows (PowerShell):
```powershell
$env:VC_CODE='ABCD-EFGH'; iex (irm https://auth.makscee.ru/vc/install.ps1)
```

## Usage

```
vc                    # launch the active harness with relay env injected
vc login              # authenticate (reads $VC_CODE or prompts)
vc login --device     # device-code flow
vc logout             # wipe credentials
vc status             # show auth / relay / provider / harness / version info
vc update             # self-update to latest release
vc --version          # print version
```

## Passing flags to the active harness

Use `--` (double-dash) to forward any flag directly to the selected harness:

```bash
vc -- --dangerously-skip-permissions
vc -- --debug --verbose
```

Any flag that the active harness accepts can be passed this way. Flags before `--` are handled by `vc`; everything after `--` is forwarded verbatim.

## Environment overrides

| Variable | Default | Purpose |
|---|---|---|
| `VC_RELAY_HOST` | `relay.makscee.ru:8448` | relay host:port |
| `VC_AUTH_HOST` | `https://auth.makscee.ru` | void-auth base URL |
| `VC_RELAY_CA` | _(embedded)_ | filesystem path to relay CA PEM |
| `VC_CODE` | _(none)_ | access code for `vc login` |

## Token storage

`~/.void-code/token` — mode 0600. Run `vc logout` to wipe.

## Release artifacts

Published to [GitHub Releases](https://github.com/makscee/void-code/releases) on every `v*.*.*` tag:

| Binary | Platform |
|---|---|
| `vc-darwin-arm64` | macOS Apple Silicon |
| `vc-darwin-amd64` | macOS Intel |
| `vc-linux-arm64` | Linux ARM64 |
| `vc-linux-amd64` | Linux x86-64 |
| `vc-windows-arm64.exe` | Windows ARM64 |
| `vc-windows-amd64.exe` | Windows x86-64 |

## Development

```bash
go build -ldflags "-X github.com/makscee/void-code/internal/version.Version=dev" ./cmd/vc
go test ./...
```

Design canon: `vault/projects/void-code/CONTEXT.md` · ADR: `docs/adr/0002-void-code-fresh-harness.md`

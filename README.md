# void-code

`vc` — relay harness for Claude Code, OpenAI Codex, and Pi. Routes your selected coding harness through void-relay for authentication and provider selection.

## Install

```bash
curl -fsSL https://auth.makscee.ru/vc/install.sh | VC_CODE=ABCD-EFGH sh
```

Default install provisions `vc`, Node.js, and Pi only. Optional harness CLIs:

```bash
sh install.sh --with-claude --with-codex     # add Claude Code and Codex
VC_INSTALL_PI=0 sh install.sh --with-codex   # Codex only
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
vc doctor             # check selected harness binary and compatibility matrix
vc update             # self-update to latest release
vc desktop-session --node /absolute/node --pi-entry /absolute/pi/cli.js -- --session-id <id>
vc --version          # print version
```

## Compatibility matrix

| Harness | Providers |
|---|---|
| Claude Code | DeepSeek relay |
| OpenAI Codex | ChatGPT relay |
| Pi | DeepSeek relay, ChatGPT relay |

Plain/native and named-key providers are not valid vc matrix rows.

## Passing flags to the active harness

Use `--` (double-dash) to forward any flag directly to the selected harness:

```bash
vc -- --dangerously-skip-permissions
vc -- --debug --verbose
```

Any flag that the active harness accepts can be passed this way. Flags before `--` are handled by `vc`; everything after `--` is forwarded verbatim.

## Desktop private-runtime contract

`desktop-session` is the non-persistent desktop launch seam. It requires absolute paths to a package-owned Node executable and Pi CLI entrypoint, launches Node directly (without a shell or `PATH` lookup), inherits stdio and the current working directory, and forwards Pi session arguments after `--`. It uses the existing vc credential, grant, relay, and managed Pi provider paths while leaving the selected harness/provider and update preferences unchanged. Desktop launches do not perform vc or harness self-update checks.

```bash
vc desktop-session \
  --node "/Applications/Void Code.app/Contents/Resources/node/bin/node" \
  --pi-entry "/Applications/Void Code.app/Contents/Resources/pi/dist/cli.js" \
  -- --session-id 550e8400-e29b-41d4-a716-446655440000
```

The desktop main process must supply package-owned paths; renderer input must not control them. Pi keeps its normal user configuration and session directory.

## Environment overrides

| Variable | Default | Purpose |
|---|---|---|
| `VC_RELAY_HOST` | `relay.makscee.ru:443` | relay host:port |
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

Design canon: `hub/vault/projects/void-code/CONTEXT.md` · ADR: `docs/adr/0002-void-code-fresh-harness.md`

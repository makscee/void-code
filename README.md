# void-code

`vc` opens the Pi console using one void-code subscription. Pi's native interface owns model selection; VC has no provider, harness, or model selector.

## Install

```bash
curl -fsSL https://auth.makscee.ru/vc/install.sh | sh
vc login
vc
```

Windows (PowerShell):

```powershell
iex (irm https://auth.makscee.ru/vc/install.ps1)
vc login
vc
```

## Usage

```text
vc                    # open Pi
vc login              # authenticate
vc logout             # wipe credentials
vc status             # show subscription/relay/version state
vc doctor             # check Pi setup
vc update             # self-update
vc -- --help          # Pi's native help
```

`desktop-session` is the hardened private Pi runtime seam used by the desktop product. It accepts only session lifecycle flags after `--`; Pi's own UI controls models after the session opens.

## Environment overrides

| Variable | Default | Purpose |
|---|---|---|
| `VC_RELAY_HOST` | `relay.makscee.ru:443` | relay host:port |
| `VC_AUTH_HOST` | `https://auth.makscee.ru` | auth base URL |
| `VC_RELAY_CA` | _(embedded)_ | relay CA PEM path |

## Token storage

`~/.void-code/token` (mode 0600). `vc logout` wipes it.

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

See [console simplification notes](docs/console-simplification-deletion-map.md).

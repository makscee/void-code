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
```

`vc` accepts no Pi launch arguments; Pi's native UI controls models and permissions after the session opens. `desktop-session` is the hardened private Pi runtime seam used by the desktop product and accepts only session lifecycle flags after `--`.

## Environment overrides

| Variable | Default | Purpose |
|---|---|---|
| `VC_RELAY_HOST` | `relay.makscee.ru:443` | relay host:port |
| `VC_AUTH_HOST` | `https://auth.makscee.ru` | auth base URL |
| `VC_RELAY_CA` | _(embedded)_ | relay CA PEM path |

## Runtime and token trust model

VC launches only its fixed Pi package entrypoint below `~/.void-code/runtime/pi`, never a `pi` selected from `PATH`. The installer checks that same platform entrypoint (Unix `dist/cli.js`; Windows npm's `.bin/pi.cmd`). This prevents accidental or less-authority PATH/symlink redirection; it is not executable provenance or a race-safe defense against the same OS user. That user can already replace files under `~/.void-code` and read the token.

On Windows npm's generated `.cmd` shim uses the user-installed Node runtime. VC does not claim to bundle or verify Node because current packaging does not install a managed Node executable.

`~/.void-code/token` is mode 0600. `vc logout` wipes it.

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

### Verifying the Windows pin

The Windows resource pin names bytes that ship to users, so it states where they
came from: a release, the commit it was built from, and the CLI revision inside
it. Check it against what the release actually published:

```bash
cd desktop
npm run qualify:windows-pin
```

It reads the release's `SHA256SUMS` over plain HTTPS — no CLI to install and no
token to hold, because whoever doubts the pin should be able to check it without
credentials. Run it whenever the pin or the release changes.

See [console simplification notes](docs/console-simplification-deletion-map.md).

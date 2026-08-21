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

### Desktop

The desktop test suite reads the pinned Node archive from an ignored local cache,
so a fresh clone needs one bootstrap step before `npm test` can pass:

```bash
cd desktop
npm ci
npm run setup   # fetches the pinned Node archive and verifies its SHA-256
npm run build
npm test
```

`npm run setup` takes both the URL and the expected digest from
`desktop/scripts/resource-pins.json`, so bumping the pin moves every consumer at
once. It is idempotent: an authentic cached archive is left alone, a corrupted one
is replaced, and bytes whose digest does not match the pin are rejected without
entering the cache. `provision-pinned-pi-smoke.sh` calls the same command, so CI
and a developer machine populate the cache through one code path.

See [console simplification notes](docs/console-simplification-deletion-map.md).

# macOS / Linux Setup Guide

## Install VC and Pi

```sh
curl -fsSL https://auth.makscee.ru/vc/install.sh | sh
```

The installer installs VC and its Pi runtime support. Open a new terminal (or run `source ~/.zshrc` on zsh) so `~/.void-code/bin` is on `PATH`.

## Log in and launch

```sh
vc login
vc
```

VC opens Pi with your void-code subscription. Pi's native interface selects models.

## Troubleshooting

- **`vc` not found:** open a new terminal, run `source ~/.zshrc`, or rerun the installer.
- **Pi unavailable:** rerun the installer, then run `vc doctor`.
- **Subscription verification:** run `vc status`; it verifies the token with the subscription service.

# Windows Setup Guide

## Install VC and Pi

In PowerShell:

```powershell
irm https://auth.makscee.ru/vc/install.ps1 | iex
```

This downloads `vc.exe`, installs it to `%USERPROFILE%\.void-code\bin\`, configures your user PATH, and provisions Pi support.

> **Open a new terminal** after the installer finishes — the PATH update only takes effect in new windows. If VS Code was open during installation, fully exit all VS Code windows and `Code.exe` processes, then reopen it.

## Log in and launch

```powershell
vc login
vc
```

VC opens Pi with your void-code subscription. Pi's native interface selects models.

## Troubleshooting

- **`vc` not found:** Restart PowerShell, or run `& "$env:USERPROFILE\.void-code\bin\vc.exe" status`.
- **Pi unavailable:** rerun the installer, then run `vc doctor`.
- **Subscription verification:** run `vc status`; it verifies the token with the subscription service.

---

> Setup doc: https://auth.makscee.ru/vc/install.ps1 installs vc automatically (Steps 3+).

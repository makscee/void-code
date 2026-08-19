# Windows Setup Guide

## Install VC and Pi

In PowerShell:

```powershell
irm https://auth.makscee.ru/vc/install.ps1 | iex
```

This downloads `vc.exe`, installs it to `%USERPROFILE%\.void-code\bin\`, repairs your user PATH, refreshes the current PowerShell PATH, and provisions managed Pi support.

> If you installed from VS Code and the installer warns about stale PATH state, fully exit all VS Code windows and `Code.exe` processes, then reopen VS Code. The installer never closes VS Code automatically.

## Log in and launch

```powershell
vc login
vc
```

VC opens Pi with your void-code subscription. Pi's native interface selects models.

## Troubleshooting

**`vc` is not recognized:**
Do not reinstall or log in again. Run the installed binary directly:

```powershell
& "$env:USERPROFILE\.void-code\bin\vc.exe" status
```

If VS Code was open during installation, fully exit all VS Code windows and `Code.exe` processes, then reopen VS Code and try `vc` again.

**Pi is unavailable:**
Run `vc doctor`. VC validates and launches its managed Pi runtime rather than trusting an unrelated `pi` command on PATH.

**Subscription verification:**
Run `vc status`; it verifies the token with the subscription service.

---

> Setup doc: https://auth.makscee.ru/vc/install.ps1 installs vc automatically (Steps 3+).

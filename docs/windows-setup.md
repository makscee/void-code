# Windows Setup Guide

Step-by-step setup for a bare Windows 10/11 machine. Takes about 5 minutes.

---

## Step 1 — Install Node.js

Go to https://nodejs.org and download the **LTS** installer. Run it with default settings.

> **Restart your terminal after this step** — Node adds itself to PATH, but the change only takes effect in new windows.

---

## Step 2 — Install claude-code

Open a **new** PowerShell or Command Prompt window (important — must be fresh after Node install):

```
npm install -g @anthropic-ai/claude-code
```

> **Restart your terminal again** after this step.

---

## Step 3 — Install vc

In a PowerShell window, run:

```powershell
irm https://auth.makscee.ru/vc/install.ps1 | iex
```

This downloads `vc.exe`, installs it to `%USERPROFILE%\.void-code\bin\`, and adds it to your user PATH.

> **Open a new terminal** after the installer finishes — the PATH update only takes effect in new windows.

---

## Step 4 — Log in

In a **new** PowerShell window, start the interactive authentication flow:

```
vc login
```

Follow the prompts to complete login.

---

## Step 5 — Start a session

```
vc
```

That's it. vc launches claude with your relay connection.

---

## Troubleshooting

**`vc` is not recognized:**
Open a brand-new PowerShell window (close and reopen). If it still fails, run the installer again: `irm https://auth.makscee.ru/vc/install.ps1 | iex`

**`claude` is not found after install:**
Open a new terminal window, then retry Step 2. Node's PATH update requires a fresh shell.

**Something else wrong:**
Run `vc doctor` — it checks your setup and tells you what's missing.

---

> Setup doc: https://auth.makscee.ru/vc/install.ps1 installs vc automatically (Steps 3+).

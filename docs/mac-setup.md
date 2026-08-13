# macOS / Linux Setup Guide

Step-by-step setup for a bare Mac (or Linux) machine. Takes about 5 minutes.

---

## Step 1 — Install Node.js

Go to https://nodejs.org and download the **LTS** installer. Run it with default settings.

Or, if you have Homebrew:

```sh
brew install node
```

> **Open a new terminal after this step** — Node adds itself to PATH, but the change only takes effect in new windows.

---

## Step 2 — Install claude-code

Open a **new** terminal window (important — must be fresh after Node install):

```sh
npm install -g @anthropic-ai/claude-code
```

> **Open a new terminal again** after this step.

---

## Step 3 — Install vc

In a terminal, run:

```sh
curl -fsSL https://auth.makscee.ru/vc/install.sh | sh
```

This downloads `vc`, installs it to `~/.void-code/bin/`, appends that directory to your PATH in `~/.zshrc` (or `~/.bash_profile` on bash), and provisions the relay CA.

> **Open a new terminal** after the installer finishes — or run:
> ```sh
> source ~/.zshrc
> ```

---

## Step 4 — Log in

In a **new** terminal, start the interactive authentication flow:

```sh
vc login
```

Follow the prompts to complete login.

---

## Step 5 — Start a session

```sh
vc
```

That's it. vc launches claude with your relay connection.

---

## Troubleshooting

**`vc` is not recognized:**
Open a brand-new terminal window (close and reopen). If it still fails, run:
```sh
source ~/.zshrc
```
or re-run the installer.

**`claude` is not found after install:**
Open a new terminal window, then retry Step 2. npm's PATH update requires a fresh shell.

**Something else wrong:**
Run `vc doctor` — it checks your setup and tells you what's missing.

---

> The installer at `https://auth.makscee.ru/vc/install.sh` handles Step 3 automatically. Run `vc login` interactively afterward.

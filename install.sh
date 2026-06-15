#!/bin/sh
# vc/install.sh — installs the void-code relay launcher `vc`, provisions
# the production relay CA, and bootstraps node + @anthropic-ai/claude-code
# (needed for `claude` which vc delegates to). Bootstraps everything —
# node runtime auto-installed on bare machines via official installer.
#
# Usage (recommended):
#   curl -fsSL https://auth.makscee.ru/vc/install.sh | VC_CODE=ABCD-EFGH sh
#
# Or if curl is not available (e.g. fresh debian LXC ships wget only):
#   wget -qO- https://auth.makscee.ru/vc/install.sh | VC_CODE=ABCD-EFGH sh
#
# Env:
#   VC_CODE             access code (required for vc login). Wiped from env
#                       after use — never echoed or written to disk.
#   VC_AUTH_HOST        default https://auth.makscee.ru — overrides every
#                       fetch URL. Used by e2e harness to point at staging.
#   VC_SKIP_DOWNLOAD    set to 1 to skip vc binary download + PATH-append
#                       (still runs node+claude check). Used by tests.
#   VC_INSTALL_DRY_RUN  set to 1 to print URLs + commands that would run,
#                       then exit 0. No downloads, no filesystem writes.
#
# Flags:
#   --dry-run           same as VC_INSTALL_DRY_RUN=1
#   -y / --yes          non-interactive: auto-confirm all prompts
#                       (used by scripted one-liner installs)
set -eu

DRY_RUN=0
YES=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    -y|--yes)  YES=1 ;;
  esac
done
[ "${VC_INSTALL_DRY_RUN:-0}" = "1" ] && DRY_RUN=1
[ "${VC_INSTALL_YES:-0}"      = "1" ] && YES=1

AUTH_HOST="${VC_AUTH_HOST:-https://auth.makscee.ru}"
VC_DIR="$HOME/.void-code"
BIN_DIR="$VC_DIR/bin"
CA_DIR="$VC_DIR"

# Minimum node major version required by @anthropic-ai/claude-code (engines.node >=18.0.0)
MIN_NODE_MAJOR=18

VERSION_JSON_URL="$AUTH_HOST/vc/version.json"
RELAY_CA_URL="$AUTH_HOST/vc/relay-ca.pem"

# ── HTTP fetcher ─────────────────────────────────────────────────────────────
# Prefer curl; fall back to wget (fresh debian/ubuntu LXC ships wget only).
if command -v curl >/dev/null 2>&1; then
  fetch_to_file()   { curl -fsSL "$1" -o "$2"; }
  fetch_to_stdout() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch_to_file()   { wget -qO "$2" "$1"; }
  fetch_to_stdout() { wget -qO- "$1"; }
else
  printf 'vc: neither curl nor wget found — install one and re-run.\n' >&2
  exit 1
fi

# ── OS + arch detection ───────────────────────────────────────────────────────
detect_os() {
  case "$(uname -s)" in
    Linux)  printf 'linux'  ;;
    Darwin) printf 'darwin' ;;
    *)      printf 'unknown' ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  printf 'amd64' ;;
    aarch64|arm64) printf 'arm64' ;;
    *)             printf 'unknown' ;;
  esac
}

OS=$(detect_os)
ARCH=$(detect_arch)

# ── version.json → canonical artifact path ────────────────────────────────────
# Fetch version.json to resolve the canonical binary path.
# Fall back to the predictable naming if the fetch fails.
VC_ARTIFACT_PATH="bin/vc-${OS}-${ARCH}"
VERSION_BANNER="==> void-code installer"

_vj_raw=""
_vj_raw="$(fetch_to_stdout "$VERSION_JSON_URL" 2>/dev/null)" || true
if [ -n "$_vj_raw" ]; then
  _ver="$(printf '%s' "$_vj_raw" | grep -o '"version"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | grep -o '"[^"]*"$' | tr -d '"')" || true
  if [ -n "$_ver" ]; then
    VERSION_BANNER="==> void-code installer (v${_ver})"
  fi
  # Try to resolve artifact path from "darwin/amd64" or "darwin-amd64" key
  _artifact="$(printf '%s' "$_vj_raw" | grep -o "\"${OS}/${ARCH}\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | head -1 | grep -o '"[^"]*"$' | tr -d '"')" || true
  if [ -z "$_artifact" ]; then
    _artifact="$(printf '%s' "$_vj_raw" | grep -o "\"${OS}-${ARCH}\"[[:space:]]*:[[:space:]]*\"[^\"]*\"" | head -1 | grep -o '"[^"]*"$' | tr -d '"')" || true
  fi
  if [ -n "$_artifact" ]; then
    VC_ARTIFACT_PATH="$_artifact"
  fi
fi

VC_BIN_URL="$AUTH_HOST/vc/$VC_ARTIFACT_PATH"

# ── shell rc file detection + idempotent PATH append ─────────────────────────
# Detect login shell, pick the right rc file. CREATE it if absent.
# Returns "fish" for fish shell (handled separately by append_path_fish).
detect_rc_file() {
  # $SHELL is set by the login shell on macOS and most Linux.
  _shell_bin="${SHELL:-}"
  if [ -z "$_shell_bin" ]; then
    _shell_bin="$(getent passwd "$(id -un)" 2>/dev/null | cut -d: -f7)" || true
  fi
  case "$_shell_bin" in
    */zsh)
      printf '%s/.zshrc' "$HOME"
      ;;
    */bash)
      # bash on macOS uses ~/.bash_profile for login shells; prefer it.
      # BUT also write to ~/.bashrc so non-login (interactive) tabs pick up PATH.
      if [ "$(uname -s)" = "Darwin" ]; then
        printf '%s/.bash_profile' "$HOME"
      else
        printf '%s/.bashrc' "$HOME"
      fi
      ;;
    */fish)
      # Handled by append_path_fish; return sentinel so caller knows.
      printf 'fish'
      ;;
    *)
      # Unknown shell — fall back to ~/.profile (POSIX).
      printf '%s/.profile' "$HOME"
      ;;
  esac
}

# Append BIN_DIR to PATH in rc_file, idempotently (marker-guarded).
# Creates the file if it does not exist.
append_path_to_rc() {
  _rc_file="$1"
  _bin="$2"
  _marker="# added by vc installer"

  # fish is handled separately by append_path_fish.
  if [ "$_rc_file" = "fish" ]; then
    append_path_fish "$_bin"
    return $?
  fi

  # Already in the current PATH? Skip rc mutation but still print hint.
  case ":${PATH}:" in
    *":${_bin}:"*) return 0 ;;
  esac

  if [ -z "$_rc_file" ]; then
    # unknown shell — print guidance only.
    printf '\nvc: PATH note: add %s to your shell PATH manually.\n' "$_bin" >&2
    return 0
  fi

  # Check for existing block via marker (idempotent guard).
  if [ -f "$_rc_file" ] && grep -qF "$_marker" "$_rc_file" 2>/dev/null; then
    # Block already present — nothing to write.
    return 0
  fi

  # Create rc file if absent (fresh-Mac ~/.zshrc case).
  if [ ! -f "$_rc_file" ]; then
    touch "$_rc_file"
    printf 'vc: created %s\n' "$_rc_file" >&2
  fi

  printf '\n%s\nexport PATH="%s:$PATH"\n' "$_marker" "$_bin" >> "$_rc_file"
  printf 'vc: added %s to PATH in %s\n' "$_bin" "$_rc_file" >&2

  # On Darwin bash: also ensure ~/.bashrc sources ~/.bash_profile so non-login
  # (interactive) tabs pick up PATH — idempotent marker-guarded append.
  if [ "$(uname -s)" = "Darwin" ] && [ "$_rc_file" = "$HOME/.bash_profile" ]; then
    _bashrc="$HOME/.bashrc"
    _bashrc_marker="# vc: source bash_profile for PATH (added by vc installer)"
    if ! [ -f "$_bashrc" ] || ! grep -qF "$_bashrc_marker" "$_bashrc" 2>/dev/null; then
      [ -f "$_bashrc" ] || touch "$_bashrc"
      printf '\n%s\n[ -f ~/.bash_profile ] && . ~/.bash_profile\n' "$_bashrc_marker" >> "$_bashrc"
      printf 'vc: added bash_profile source to %s\n' "$_bashrc" >&2
    fi
  fi
}

# Append BIN_DIR to fish PATH in ~/.config/fish/config.fish, idempotently.
append_path_fish() {
  _bin="$1"
  _fish_marker="# added by vc installer"
  _fish_cfg_dir="$HOME/.config/fish"
  _fish_cfg="$_fish_cfg_dir/config.fish"

  # Already in current PATH? Nothing to do.
  case ":${PATH}:" in
    *":${_bin}:"*) return 0 ;;
  esac

  # Already written (idempotent check).
  if [ -f "$_fish_cfg" ] && grep -qF "$_fish_marker" "$_fish_cfg" 2>/dev/null; then
    return 0
  fi

  mkdir -p "$_fish_cfg_dir"
  [ -f "$_fish_cfg" ] || touch "$_fish_cfg"
  printf '\n%s\nset -gx PATH %s $PATH\n' "$_fish_marker" "$_bin" >> "$_fish_cfg"
  printf 'vc: added %s to PATH in %s\n' "$_bin" "$_fish_cfg" >&2
}

# ── node version helper ───────────────────────────────────────────────────────
# Prints the node major version integer, or empty string if node is absent/unparseable.
node_major() {
  if ! command -v node >/dev/null 2>&1; then
    printf ''
    return
  fi
  _ver="$(node --version 2>/dev/null | head -1 | tr -d 'v' | cut -d. -f1)" || true
  printf '%s' "${_ver:-}"
}

# ── node bootstrap ────────────────────────────────────────────────────────────
# ensure_node: guarantees node >= MIN_NODE_MAJOR is present, installing if needed.
# Returns 0 on success, 1 on unrecoverable failure (caller must abort).
ensure_node() {
  _cur_major="$(node_major)"

  if [ -n "$_cur_major" ] && [ "$_cur_major" -ge "$MIN_NODE_MAJOR" ] 2>/dev/null; then
    printf 'vc: node OK (v%s)\n' "$(node --version 2>/dev/null | tr -d 'v')" >&2
    return 0
  fi

  if [ -n "$_cur_major" ]; then
    printf 'vc: node v%s found but requires >=%s — will install updated version\n' \
      "$(node --version 2>/dev/null | tr -d 'v')" "$MIN_NODE_MAJOR" >&2
  else
    printf 'vc: node / npm not found — bootstrapping node runtime\n' >&2
  fi

  if command -v brew >/dev/null 2>&1; then
    printf 'vc: installing node via Homebrew...\n' >&2
    if [ "$DRY_RUN" = 1 ]; then
      printf 'WOULD: brew install node\n'
      return 0
    fi
    brew install node >&2 || {
      printf 'vc: brew install node failed. Visit https://nodejs.org to install manually.\n' >&2
      return 1
    }
  else
    # No Homebrew — download the official Apple-notarized Node LTS .pkg from
    # nodejs.org. The macOS .pkg is UNIVERSAL (one file, both arches) — named
    # node-vX.Y.Z.pkg with NO -darwin-<arch> suffix (the arch-suffixed names are
    # tarballs only). Resolve the CURRENT v22 patch from the index, because the
    # filename changes every release — a hardcoded patch (e.g. v22.14.0) 404s once
    # Node moves on. This was the live break behind the fresh-Mac onboarding fails.
    # Fall back to a known-good only if the index fetch itself fails.
    _node_base="https://nodejs.org/dist/latest-v22.x"
    _node_file="$(fetch_to_stdout "$_node_base/SHASUMS256.txt" 2>/dev/null \
      | grep -o 'node-v[0-9][0-9.]*\.pkg' | head -1)" || true
    [ -z "$_node_file" ] && _node_file="node-v22.22.3.pkg"
    _node_lts_url="$_node_base/$_node_file"
    _node_tmp="$(mktemp /tmp/node-installer-XXXXXX.pkg)"

    if [ "$DRY_RUN" = 1 ]; then
      printf 'WOULD: download %s\n' "$_node_lts_url"
      printf 'WOULD: sudo installer -pkg <node.pkg> -target /\n'
      rm -f "$_node_tmp"
      return 0
    fi

    printf 'vc: downloading Node.js LTS installer from nodejs.org...\n' >&2
    fetch_to_file "$_node_lts_url" "$_node_tmp" || {
      printf 'vc: failed to download Node.js installer from %s\n' "$_node_lts_url" >&2
      printf '    Visit https://nodejs.org to install Node.js manually, then re-run.\n' >&2
      rm -f "$_node_tmp"
      return 1
    }

    _pkg_size=0
    command -v wc >/dev/null 2>&1 && _pkg_size="$(wc -c < "$_node_tmp" 2>/dev/null | tr -d ' ')" || true
    if [ -n "$_pkg_size" ] && [ "$_pkg_size" -lt 1024 ] 2>/dev/null; then
      printf 'vc: downloaded installer looks too small (%s bytes) — aborting node install\n' "$_pkg_size" >&2
      rm -f "$_node_tmp"
      return 1
    fi

    printf 'vc: running Node.js installer (one admin/sudo prompt)...\n' >&2
    sudo installer -pkg "$_node_tmp" -target / >&2 || {
      printf 'vc: Node.js installer failed or was cancelled.\n' >&2
      printf '    Visit https://nodejs.org to install Node.js manually, then re-run.\n' >&2
      rm -f "$_node_tmp"
      return 1
    }
    rm -f "$_node_tmp"

    # Refresh PATH so newly installed node is found.
    export PATH="/usr/local/bin:/usr/local/sbin:$PATH"
  fi

  # Re-verify after install.
  _new_major="$(node_major)"
  if [ -n "$_new_major" ] && [ "$_new_major" -ge "$MIN_NODE_MAJOR" ] 2>/dev/null; then
    printf 'vc: node installed successfully (v%s)\n' "$(node --version 2>/dev/null | tr -d 'v')" >&2
    return 0
  else
    printf 'vc: node install appeared to succeed but node >= %s not found in PATH.\n' "$MIN_NODE_MAJOR" >&2
    printf '    Open a new terminal and re-run, or visit https://nodejs.org.\n' >&2
    return 1
  fi
}

# Check claude binary; offer/prompt npm install if absent.
# Returns 0 if claude is present (or successfully installed), 1 if still absent.
check_claude() {
  if command -v claude >/dev/null 2>&1 || [ -x "$HOME/.void-code/bin/claude" ]; then
    printf 'vc: claude already installed\n' >&2
    return 0
  fi

  if ! command -v npm >/dev/null 2>&1; then
    printf '\nvc: claude not found, and npm is not available.\n' >&2
    printf '    Install Node.js first (see above), then re-run.\n' >&2
    return 1
  fi

  printf '\nvc: @anthropic-ai/claude-code (claude) is not installed.\n' >&2

  _do_install=0
  if [ "$YES" = 1 ]; then
    _do_install=1
  else
    # Prompt only if stdin is a terminal (not piped).
    if [ -t 0 ]; then
      printf '    Install it now with npm? [Y/n] ' >&2
      read -r _ans </dev/tty
      case "$_ans" in
        [Nn]*) _do_install=0 ;;
        *)     _do_install=1 ;;
      esac
    else
      # Non-interactive (piped) without -y: print guidance and continue.
      printf '    Run: npm install -g @anthropic-ai/claude-code\n' >&2
      printf '    Then open a new terminal and run: vc\n' >&2
      return 1
    fi
  fi

  if [ "$_do_install" = 1 ]; then
    printf 'vc: installing @anthropic-ai/claude-code via npm...\n' >&2
    if npm install -g @anthropic-ai/claude-code >&2; then
      printf 'vc: @anthropic-ai/claude-code installed.\n' >&2
      printf '    Note: open a new terminal before running vc (npm PATH update).\n' >&2
      return 0
    else
      printf 'vc: npm install failed.\n' >&2
      printf '    Run manually: npm install -g @anthropic-ai/claude-code\n' >&2
      return 1
    fi
  else
    printf '    Run when ready: npm install -g @anthropic-ai/claude-code\n' >&2
    return 1
  fi
}

# ── relay CA → OS trust store ─────────────────────────────────────────────────
# vc injects NODE_EXTRA_CA_CERTS so *Node* (claude) trusts the relay's HTTPS
# proxy cert — but tools the agent shells out to (curl, git, python) use the OS
# trust store and otherwise fail the proxy TLS hop with:
#   curl: (60) SSL certificate problem: unable to get local issuer certificate
# Teach the OS to trust the relay CA. Non-fatal + idempotent: a failure here
# leaves vc fully working; only in-session curl/git would need a manual trust.
trust_relay_ca() {
  _ca="$CA_DIR/relay-ca.pem"
  [ -f "$_ca" ] || return 0

  if [ "$(uname -s)" = "Darwin" ]; then
    _kc="$HOME/Library/Keychains/login.keychain-db"
    [ -f "$_kc" ] || _kc="$HOME/Library/Keychains/login.keychain"
    if security add-trusted-cert -r trustRoot -k "$_kc" "$_ca" >/dev/null 2>&1; then
      printf '==> trusted relay CA in login keychain\n' >&2
    else
      printf 'vc: could not auto-trust relay CA in keychain — in-session curl/git may show SSL errors.\n' >&2
      printf '    Fix manually: security add-trusted-cert -r trustRoot -k %s %s\n' "$_kc" "$_ca" >&2
    fi
    return 0
  fi

  # Linux — need root to write the system anchor dir.
  _sudo=""
  if [ "$(id -u)" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then _sudo="sudo"; else
      printf 'vc: not root and no sudo — skipping OS CA trust (in-session curl/git may show SSL errors).\n' >&2
      return 0
    fi
  fi

  if command -v update-ca-certificates >/dev/null 2>&1; then
    # Debian / Ubuntu / Alpine — anchor must end in .crt
    if $_sudo install -m 0644 "$_ca" /usr/local/share/ca-certificates/void-relay-ca.crt 2>/dev/null \
       && $_sudo update-ca-certificates >/dev/null 2>&1; then
      printf '==> trusted relay CA via update-ca-certificates\n' >&2
    else
      printf 'vc: update-ca-certificates failed — in-session curl/git may show SSL errors.\n' >&2
    fi
  elif command -v update-ca-trust >/dev/null 2>&1; then
    # RHEL / Fedora / CentOS
    if $_sudo install -m 0644 "$_ca" /etc/pki/ca-trust/source/anchors/void-relay-ca.pem 2>/dev/null \
       && $_sudo update-ca-trust extract >/dev/null 2>&1; then
      printf '==> trusted relay CA via update-ca-trust\n' >&2
    else
      printf 'vc: update-ca-trust failed — in-session curl/git may show SSL errors.\n' >&2
    fi
  else
    printf 'vc: no known CA-trust tool found — in-session curl/git may show SSL errors.\n' >&2
    printf '    Add %s to your system trust store manually.\n' "$_ca" >&2
  fi
}

# ── dry-run ───────────────────────────────────────────────────────────────────
if [ "$DRY_RUN" = 1 ]; then
  printf '%s\n' "$VERSION_BANNER"
  printf 'GET %s  (-> %s/vc)\n' "$VC_BIN_URL" "$BIN_DIR"
  printf 'GET %s  (-> %s/relay-ca.pem)\n' "$RELAY_CA_URL" "$CA_DIR"
  if [ "$(uname -s)" = "Darwin" ]; then
    printf 'WOULD: security add-trusted-cert -r trustRoot -k ~/Library/Keychains/login.keychain-db <relay-ca.pem>\n'
  else
    printf 'WOULD: install relay-ca.pem to system anchors + update-ca-certificates / update-ca-trust\n'
  fi
  _dry_major="$(node_major)"
  if [ -z "$_dry_major" ] || ! [ "$_dry_major" -ge "$MIN_NODE_MAJOR" ] 2>/dev/null; then
    if command -v brew >/dev/null 2>&1; then
      printf 'WOULD: brew install node\n'
    else
      printf 'WOULD: download https://nodejs.org/dist/latest-v22.x/node-v<latest>.pkg  (universal)\n'
      printf 'WOULD: sudo installer -pkg <node.pkg> -target /\n'
    fi
  fi
  if ! command -v claude >/dev/null 2>&1 && ! [ -x "$HOME/.void-code/bin/claude" ]; then
    printf 'WOULD: npm install -g @anthropic-ai/claude-code\n'
  fi
  _dry_rc="$(detect_rc_file)"
  printf 'RC file: %s\n' "${_dry_rc}"
  if [ -n "${VC_CODE:-}" ]; then
    printf 'WOULD: vc login  (VC_CODE set)\n'
  fi
  exit 0
fi

# ── main install ──────────────────────────────────────────────────────────────
printf '%s\n' "$VERSION_BANNER"
printf '==> detecting platform: %s/%s\n' "$OS" "$ARCH"

# 1. Download vc binary + provision relay CA
mkdir -p "$BIN_DIR"

if [ "${VC_SKIP_DOWNLOAD:-0}" != "1" ]; then
  printf '==> downloading vc binary from %s\n' "$AUTH_HOST" >&2
  TMP_BIN="$(mktemp)"
  fetch_to_file "$VC_BIN_URL" "$TMP_BIN" \
    || { printf 'vc: failed to download %s\n' "$VC_BIN_URL" >&2; rm -f "$TMP_BIN"; exit 1; }

  # Basic sanity check: binary should be > 1 KB.
  _bin_size=0
  if command -v wc >/dev/null 2>&1; then
    _bin_size="$(wc -c < "$TMP_BIN" 2>/dev/null | tr -d ' ')" || true
  fi
  if [ -n "$_bin_size" ] && [ "$_bin_size" -lt 1024 ] 2>/dev/null; then
    printf 'vc: download looks too small (%s bytes) — aborting\n' "$_bin_size" >&2
    rm -f "$TMP_BIN"; exit 1
  fi

  chmod 0755 "$TMP_BIN"
  mv -f "$TMP_BIN" "$BIN_DIR/vc"
  printf '==> installed to %s\n' "$BIN_DIR/vc" >&2
fi

# 2. Provision the production relay CA (public cert only)
printf '==> provisioning relay CA\n' >&2
fetch_to_file "$RELAY_CA_URL" "$CA_DIR/relay-ca.pem" \
  || { printf 'vc: failed to download relay CA\n' >&2; exit 1; }

# 2b. Trust the relay CA in the OS store so curl/git (not just node) work in-session.
trust_relay_ca

# 3. PATH — register vc FIRST, before the node/claude bootstrap. The vc launcher
# (vc login, vc doctor) works without node; a node/claude hiccup must NEVER leave
# vc off PATH. Previously this ran AFTER node bootstrap, so a node failure's
# `exit 1` skipped it entirely → vc installed but unreachable on fresh machines.
if [ "${VC_SKIP_DOWNLOAD:-0}" != "1" ]; then
  RC_FILE="$(detect_rc_file)"
  append_path_to_rc "$RC_FILE" "$BIN_DIR"
fi

# 4. node/npm + claude bootstrap — NON-FATAL. On failure, vc is already installed
# and on PATH; the post-install steps print exactly what's left to finish (node +
# claude). We never abort the whole install over a node hiccup.
printf '==> bootstrapping node / claude\n' >&2
NODE_OK=0
if ensure_node; then
  NODE_OK=1
  check_claude || true
else
  printf 'vc: node bootstrap incomplete — vc itself is installed; finish node + claude per the steps below.\n' >&2
fi

# 5. vc login — use VC_CODE if provided, then wipe it from env
if [ -n "${VC_CODE:-}" ]; then
  VC_CODE_VALUE="$VC_CODE"
  unset VC_CODE
  printf '==> running first-time login...\n' >&2
  "$BIN_DIR/vc" login --code "$VC_CODE_VALUE" \
    || printf 'vc: login failed — re-run: vc login --code <YOUR-CODE>\n' >&2
  unset VC_CODE_VALUE
fi

# ── post-install UX ───────────────────────────────────────────────────────────
if [ "${VC_SKIP_DOWNLOAD:-0}" != "1" ]; then
  _claude_ok=0
  command -v claude >/dev/null 2>&1 && _claude_ok=1
  [ -x "$HOME/.void-code/bin/claude" ] && _claude_ok=1

  _vc_ok=0
  command -v vc >/dev/null 2>&1 && _vc_ok=1
  if [ "$_vc_ok" = 1 ]; then _vc_note="now"; else _vc_note="after you open a new terminal"; fi

  printf '\n'
  printf '==============================================\n'
  printf '  vc installed — reachable as `vc` %s\n' "$_vc_note"
  printf '==============================================\n'
  printf '\n'
  printf 'NEXT STEPS:\n'

  # Numbered steps, only for what is actually missing — never a silent dead end.
  _n=1
  if [ "$NODE_OK" = 0 ]; then
    printf '\n  %s. Install Node.js (required — claude runs on it):\n' "$_n"
    printf '         Download the macOS installer from https://nodejs.org and run it.\n'
    _n=$((_n + 1))
  fi
  if [ "$_claude_ok" = 0 ]; then
    printf '\n  %s. Install Claude Code:\n' "$_n"
    printf '         npm install -g @anthropic-ai/claude-code\n'
    _n=$((_n + 1))
  fi
  if [ "$_vc_ok" = 0 ] || [ "$NODE_OK" = 0 ] || [ "$_claude_ok" = 0 ]; then
    printf '\n  %s. Open a NEW terminal (picks up PATH + npm changes)\n' "$_n"
    printf '         or run: source %s\n' "${RC_FILE:-~/.zshrc}"
    _n=$((_n + 1))
  fi
  printf '\n  %s. Log in: vc login --code <YOUR-CODE-FROM-OPERATOR>\n' "$_n"
  _n=$((_n + 1))
  printf '\n  %s. Run: vc\n' "$_n"

  printf '\n'
  printf '  Stuck? Run: vc doctor\n'
  printf '\n'
fi

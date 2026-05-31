#!/bin/sh
# vc/install.sh — installs the void-code relay launcher `vc`, provisions
# the production relay CA, and guides node + @anthropic-ai/claude-code setup
# (needed for `claude` which vc delegates to).
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

if [ "$DRY_RUN" != 1 ]; then
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
fi

VC_BIN_URL="$AUTH_HOST/vc/$VC_ARTIFACT_PATH"

# ── shell rc file detection + idempotent PATH append ─────────────────────────
# Detect login shell, pick the right rc file. CREATE it if absent.
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
      if [ "$(uname -s)" = "Darwin" ]; then
        printf '%s/.bash_profile' "$HOME"
      else
        printf '%s/.bashrc' "$HOME"
      fi
      ;;
    */fish)
      # fish uses a different path syntax; guide instead of appending.
      printf ''
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

  # Already in the current PATH? Skip rc mutation but still print hint.
  case ":${PATH}:" in
    *":${_bin}:"*) return 0 ;;
  esac

  if [ -z "$_rc_file" ]; then
    # fish or unknown — print guidance only.
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
}

# ── node/npm prerequisite check + guidance ────────────────────────────────────
# Returns 0 if node is present, 1 if absent (but with guidance printed).
check_node() {
  if command -v node >/dev/null 2>&1; then
    _nv="$(node --version 2>/dev/null | head -1 || printf 'unknown')"
    printf 'vc: node already installed (%s)\n' "$_nv" >&2
    return 0
  fi

  printf '\nvc: node / npm not found.\n' >&2

  if command -v brew >/dev/null 2>&1; then
    if [ "$YES" = 1 ]; then
      printf 'vc: installing node via Homebrew (--yes flag set)...\n' >&2
      brew install node >&2
      return $?
    fi
    printf '    Homebrew is installed. Run to install Node.js:\n' >&2
    printf '        brew install node\n' >&2
    printf '    Then re-run this installer.\n' >&2
  else
    printf '    Install Node.js from https://nodejs.org (LTS version),\n' >&2
    printf '    then re-run this installer.\n' >&2
  fi
  return 1
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

# ── dry-run ───────────────────────────────────────────────────────────────────
if [ "$DRY_RUN" = 1 ]; then
  printf '%s\n' "$VERSION_BANNER"
  printf 'GET %s  (-> %s/vc)\n' "$VC_BIN_URL" "$BIN_DIR"
  printf 'GET %s  (-> %s/relay-ca.pem)\n' "$RELAY_CA_URL" "$CA_DIR"
  if ! command -v node >/dev/null 2>&1; then
    if command -v brew >/dev/null 2>&1; then
      printf 'WOULD: brew install node (prompt; or auto with -y)\n'
    else
      printf 'WOULD: guide → https://nodejs.org\n'
    fi
  fi
  if ! command -v claude >/dev/null 2>&1 && ! [ -x "$HOME/.void-code/bin/claude" ]; then
    printf 'WOULD: npm install -g @anthropic-ai/claude-code (prompt; or auto with -y)\n'
  fi
  _dry_rc="$(detect_rc_file)"
  printf 'RC file: %s\n' "${_dry_rc:-<fish/unknown — manual PATH>}"
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

# 3. node/npm + claude prerequisite checks
printf '==> checking node / claude prerequisites\n' >&2
check_node || true
check_claude || true

# 4. PATH — detect rc file, create if absent, append idempotently
if [ "${VC_SKIP_DOWNLOAD:-0}" != "1" ]; then
  RC_FILE="$(detect_rc_file)"
  append_path_to_rc "$RC_FILE" "$BIN_DIR"
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

  printf '\n'
  printf '==============================================\n'
  printf '  vc installed successfully!\n'
  printf '==============================================\n'
  printf '\n'
  printf 'NEXT STEPS:\n'

  if [ "$_claude_ok" = 0 ]; then
    printf '\n'
    printf '  1. Install claude-code (not yet installed):\n'
    printf '         npm install -g @anthropic-ai/claude-code\n'
    printf '\n'
    printf '  2. Open a NEW terminal (picks up PATH + npm changes)\n'
    printf '         source %s\n' "${RC_FILE:-~/.zshrc}"
    printf '     or just open a new terminal window.\n'
    printf '\n'
    printf '  3. Log in: vc login --code <YOUR-CODE-FROM-OPERATOR>\n'
    printf '\n'
    printf '  4. Run: vc\n'
  else
    printf '\n'
    if command -v vc >/dev/null 2>&1; then
      printf '  1. Log in: vc login --code <YOUR-CODE-FROM-OPERATOR>\n'
      printf '\n'
      printf '  2. Run: vc\n'
    else
      printf '  1. Open a NEW terminal (vc is installed — new terminal picks up PATH)\n'
      printf '         source %s\n' "${RC_FILE:-~/.zshrc}"
      printf '     or open a new terminal window.\n'
      printf '\n'
      printf '  2. Log in: vc login --code <YOUR-CODE-FROM-OPERATOR>\n'
      printf '\n'
      printf '  3. Run: vc\n'
    fi
  fi

  printf '\n'
  printf '  Stuck? Run: vc doctor\n'
  printf '\n'
fi

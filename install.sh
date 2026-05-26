#!/bin/sh
# vc/install.sh — installs the void-code relay launcher `vc`, provisions
# the production relay CA, and installs node + @anthropic-ai/claude-code
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
#                       (still runs node+claude install). Used by tests.
#   VC_INSTALL_DRY_RUN  set to 1 to print URLs + commands that would run,
#                       then exit 0. No downloads, no filesystem writes.
#
# Flags:
#   --dry-run           same as VC_INSTALL_DRY_RUN=1
set -eu

DRY_RUN=0
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
  esac
done
[ "${VC_INSTALL_DRY_RUN:-0}" = "1" ] && DRY_RUN=1

AUTH_HOST="${VC_AUTH_HOST:-https://auth.makscee.ru}"
VC_DIR="$HOME/.void-code"
BIN_DIR="$VC_DIR/bin"
CA_DIR="$VC_DIR"

RELAY_CA_URL="$AUTH_HOST/vc/relay-ca.pem"

# Pick an HTTP fetcher (curl preferred, wget fallback). Fresh debian/ubuntu LXC
# templates ship wget but not curl — the installer must work on either.
if command -v curl >/dev/null 2>&1; then
  fetch_to_file()   { curl -fsSL "$1" -o "$2"; }
  fetch_to_stdout() { curl -fsSL "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch_to_file()   { wget -qO "$2" "$1"; }
  fetch_to_stdout() { wget -qO- "$1"; }
else
  echo "vc: neither curl nor wget found — install one and re-run." >&2
  exit 1
fi

# Detect OS + arch for vc binary selection.
detect_os() {
  case "$(uname -s)" in
    Linux)  echo "linux"  ;;
    Darwin) echo "darwin" ;;
    *)      echo "unknown" ;;
  esac
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64)  echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *)             echo "unknown" ;;
  esac
}

# Detect which package manager to use for node install.
detect_node_pkg_manager() {
  if command -v apt-get >/dev/null 2>&1; then echo "apt-get"; return; fi
  if command -v dnf     >/dev/null 2>&1; then echo "dnf";     return; fi
  if command -v brew    >/dev/null 2>&1; then echo "brew";    return; fi
  echo "none"
}

# Install node if absent. Uses NodeSource v20 on apt systems (fast: ~20s vs
# 160s for Debian's ecosystem split). Falls back to dnf/brew on other systems.
install_node_if_absent() {
  if command -v node >/dev/null 2>&1; then
    echo "vc: node already installed ($(node --version 2>/dev/null || echo unknown))" >&2
    return 0
  fi
  pkgmgr=$(detect_node_pkg_manager)
  case "$pkgmgr" in
    apt-get)
      SUDO=""
      if [ "$(id -u)" != "0" ] && command -v sudo >/dev/null 2>&1; then SUDO="sudo"; fi
      if [ "$(id -u)" != "0" ] && ! command -v sudo >/dev/null 2>&1; then
        echo "vc: need root or sudo to install node; re-run as root or install node manually" >&2
        return 1
      fi
      echo "vc: installing node v20 via NodeSource…" >&2
      if command -v curl >/dev/null 2>&1; then
        curl -fsSL https://deb.nodesource.com/setup_20.x | $SUDO bash - >&2
      else
        wget -qO- https://deb.nodesource.com/setup_20.x | $SUDO bash - >&2
      fi
      $SUDO apt-get install -y nodejs >&2
      ;;
    dnf)
      echo "vc: installing node via dnf…" >&2
      SUDO=""
      if [ "$(id -u)" != "0" ] && command -v sudo >/dev/null 2>&1; then SUDO="sudo"; fi
      $SUDO dnf install -y nodejs >&2
      ;;
    brew)
      echo "vc: installing node via brew…" >&2
      brew install node >&2
      ;;
    *)
      echo "vc: no supported package manager found (apt-get/dnf/brew)" >&2
      echo "vc: install node 18+ manually and re-run: https://nodejs.org" >&2
      return 1
      ;;
  esac
}

# Install @anthropic-ai/claude-code if absent.
# Uses --prefix $HOME/.void-code so claude lands in ~/.void-code/bin/ (no root needed).
install_claude_if_absent() {
  CLAUDE_CHECK="$(command -v claude 2>/dev/null || printf '%s' "$HOME/.void-code/bin/claude")"
  if command -v claude >/dev/null 2>&1 || [ -x "$HOME/.void-code/bin/claude" ]; then
    echo "vc: claude already installed ($("$CLAUDE_CHECK" --version 2>/dev/null | head -1 || echo unknown))" >&2
    return 0
  fi
  if ! command -v npm >/dev/null 2>&1; then
    echo "vc: node/npm absent — cannot install @anthropic-ai/claude-code" >&2
    echo "vc: re-run this installer after installing node 18+: https://nodejs.org" >&2
    return 1
  fi
  CLAUDE_PREFIX="$HOME/.void-code"
  mkdir -p "$CLAUDE_PREFIX"
  echo "vc: installing @anthropic-ai/claude-code via npm (prefix: $CLAUDE_PREFIX)…" >&2
  if ! npm_config_prefix="$CLAUDE_PREFIX" npm install -g @anthropic-ai/claude-code >&2; then
    echo "vc: 'npm install -g @anthropic-ai/claude-code' failed" >&2
    echo "vc: install claude manually: npm install -g @anthropic-ai/claude-code" >&2
    return 1
  fi
  echo "vc: @anthropic-ai/claude-code installed to $CLAUDE_PREFIX/bin/claude" >&2
}

OS=$(detect_os)
ARCH=$(detect_arch)
VC_BIN_URL="$AUTH_HOST/vc/bin/vc-${OS}-${ARCH}"

if [ "$DRY_RUN" = 1 ]; then
  echo "GET $VC_BIN_URL  (-> $BIN_DIR/vc)"
  echo "GET $RELAY_CA_URL  (-> $CA_DIR/relay-ca.pem)"
  if ! command -v node >/dev/null 2>&1; then
    pkgmgr=$(detect_node_pkg_manager)
    case "$pkgmgr" in
      apt-get) echo "WOULD: NodeSource setup_20.x | bash + apt-get install -y nodejs" ;;
      dnf)     echo "WOULD: dnf install -y nodejs" ;;
      brew)    echo "WOULD: brew install node" ;;
      *)       echo "WOULD: (no package manager found — node install skipped)" ;;
    esac
  fi
  if ! command -v claude >/dev/null 2>&1 && ! [ -x "$HOME/.void-code/bin/claude" ]; then
    echo "WOULD: npm install -g @anthropic-ai/claude-code  (prefix: \$HOME/.void-code)"
  fi
  if [ -n "${VC_CODE:-}" ]; then
    echo "WOULD: vc login  (VC_CODE set)"
  fi
  exit 0
fi

# 1. Download vc binary + provision relay CA
mkdir -p "$BIN_DIR"

if [ "${VC_SKIP_DOWNLOAD:-0}" != "1" ]; then
  TMP_BIN="$(mktemp)"
  fetch_to_file "$VC_BIN_URL" "$TMP_BIN" || { echo "vc: failed to download $VC_BIN_URL" >&2; rm -f "$TMP_BIN"; exit 1; }
  chmod 0755 "$TMP_BIN"
  mv -f "$TMP_BIN" "$BIN_DIR/vc"
fi

# 2. Provision the production relay CA (public cert only)
fetch_to_file "$RELAY_CA_URL" "$CA_DIR/relay-ca.pem" || { echo "vc: failed to download relay CA" >&2; exit 1; }

# 3. Install node + @anthropic-ai/claude-code (idempotent)
# node must come before claude (npm needed for npm install).
install_node_if_absent || true
install_claude_if_absent || true

# 4. PATH — append BIN_DIR (idempotent)
if [ "${VC_SKIP_DOWNLOAD:-0}" != "1" ]; then
  case ":$PATH:" in
    *":$BIN_DIR:"*) : ;;
    *)
      for rc in "$HOME/.zshrc" "$HOME/.bashrc" "$HOME/.profile"; do
        [ -f "$rc" ] || continue
        grep -q "$BIN_DIR" "$rc" 2>/dev/null && continue
        printf '\nexport PATH="%s:$PATH"\n' "$BIN_DIR" >> "$rc"
      done
      ;;
  esac
fi

# 5. vc login — use VC_CODE if provided, then wipe it from env
if [ -n "${VC_CODE:-}" ]; then
  # Export VC_CODE into vc login, then immediately unset from THIS process
  VC_CODE_VALUE="$VC_CODE"
  unset VC_CODE
  "$BIN_DIR/vc" login --code "$VC_CODE_VALUE" || { echo "vc: login failed — re-run 'vc login' manually" >&2; }
  unset VC_CODE_VALUE
fi

# Post-install UX
if [ "${VC_SKIP_DOWNLOAD:-0}" != "1" ]; then
  if command -v vc >/dev/null 2>&1; then
    echo ""
    echo "✓ ready · run: vc"
  else
    echo ""
    echo "vc installed to $BIN_DIR."
    echo "It is not yet on your PATH in THIS shell. Open a new terminal"
    echo "(or run: source ~/.zshrc  # or: exec \$SHELL -l), then: vc"
  fi
fi

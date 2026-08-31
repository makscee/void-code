#!/bin/sh
# vc/install.sh — installs the void-code relay launcher `vc`, provisions
# the production relay CA, and bootstraps node + selected agent CLIs.
# Default: vc + node + Pi only. Optional flags install Claude Code and/or Codex.
#
# Usage (recommended):
#   curl -fsSL https://auth.makscee.ru/vc/install.sh | sh
#
# Or if curl is not available (e.g. fresh debian LXC ships wget only):
#   wget -qO- https://auth.makscee.ru/vc/install.sh | sh
#
# After installation, authenticate interactively with: vc login
#
# TWO COPIES, AND THIS ONE IS THE SOURCE. On every stable release
# .github/workflows/release.yml copies this file over
# void-auth/public/vc/install.sh — the copy served at /vc/install.sh, the one
# every `curl | sh` actually runs. So the direction is one-way: edit here, and
# the release carries it across. A fix made only in void-auth is erased by the
# next stable release, and a fix made here reaches users only once one happens,
# so an urgent one has to be applied in both places by hand. The GitHub release
# mirror below is exactly what that costs when it is forgotten: it landed in
# the served copy and never in this one, and nothing here said the other copy
# existed.
#
# Env:
#   VC_AUTH_HOST        default https://auth.makscee.ru — overrides every
#                       fetch URL. Used by e2e harness to point at staging.
#   VC_SKIP_DOWNLOAD    set to 1 to skip vc binary download + PATH-append
#                       (still runs node+claude check). Used by tests.
#   VC_INSTALL_DRY_RUN  set to 1 to print URLs + commands that would run,
#                       then exit 0. No downloads, no filesystem writes.
#   VC_INSTALL_PI       default 1; install @earendil-works/pi-coding-agent
#   VC_INSTALL_CLAUDE   default 0; install @anthropic-ai/claude-code
#   VC_INSTALL_CODEX    default 0; install @openai/codex
#   VC_MIRROR_REPO      default makscee/void-code — GitHub repo whose release
#                       serves the vc binary when VC_AUTH_HOST fails. The
#                       mirror download is always sha256-checked against the
#                       SHA256SUMS of the same release.
#
# Flags:
#   --dry-run           same as VC_INSTALL_DRY_RUN=1
#   -y / --yes          non-interactive: auto-confirm all prompts
#                       (used by scripted one-liner installs)
#   --with-pi / --without-pi
#   --with-claude
#   --with-codex
set -eu

DRY_RUN=0
YES=0
INSTALL_PI="${VC_INSTALL_PI:-1}"
INSTALL_CLAUDE="${VC_INSTALL_CLAUDE:-0}"
INSTALL_CODEX="${VC_INSTALL_CODEX:-0}"
for arg in "$@"; do
  case "$arg" in
    --dry-run) DRY_RUN=1 ;;
    -y|--yes)  YES=1 ;;
    --with-pi) INSTALL_PI=1 ;;
    --without-pi) INSTALL_PI=0 ;;
    --with-claude) INSTALL_CLAUDE=1 ;;
    --with-codex) INSTALL_CODEX=1 ;;
  esac
done
[ "${VC_INSTALL_DRY_RUN:-0}" = "1" ] && DRY_RUN=1
[ "${VC_INSTALL_YES:-0}"      = "1" ] && YES=1

AUTH_HOST="${VC_AUTH_HOST:-https://auth.makscee.ru}"
VC_DIR="$HOME/.void-code"
BIN_DIR="$VC_DIR/bin"
CA_DIR="$VC_DIR"
# Pi lives under VC's managed runtime; vc launches this fixed entrypoint, never a PATH shim.
PI_RUNTIME_DIR="$VC_DIR/runtime/pi"
PI_ENTRY="$PI_RUNTIME_DIR/node_modules/@earendil-works/pi-coding-agent/dist/cli.js"

# Minimum node major version installed for the Node-based agent CLIs (Claude Code + Pi).
MIN_NODE_MAJOR=22

VERSION_JSON_URL="$AUTH_HOST/vc/version.json"
RELAY_CA_URL="$AUTH_HOST/vc/relay-ca.pem"

# ── HTTP fetcher ─────────────────────────────────────────────────────────────
# Prefer curl; fall back to wget (fresh debian/ubuntu LXC ships wget only).
#
# The vc binary is ~8 MB and dies mid-stream on some routes
# (curl: (92) HTTP/2 stream ... INTERNAL_ERROR), so every fetch gets a retry
# budget instead of one shot:
#   - curl retries in-process (--retry). Only --retry-all-errors makes it cover
#     a mid-stream abort, and that option exists since curl 7.71 — older builds
#     abort on the unknown option, so it is probed for, never assumed.
#   - wget already retries on its own (--tries defaults to 20).
#   - fetch_to_file_retry adds whole-process attempts on top. That is the only
#     retry an old curl gets for a broken stream, so it is enabled exactly
#     where curl's own budget does not cover the failure we are chasing.
CURL_RETRY_ARGS=""
FETCH_ATTEMPTS=1
if command -v curl >/dev/null 2>&1; then
  CURL_RETRY_ARGS="--retry 3 --retry-delay 1 --retry-max-time 120"
  if curl --help all 2>/dev/null | grep -q -- '--retry-all-errors'; then
    CURL_RETRY_ARGS="$CURL_RETRY_ARGS --retry-all-errors"
  else
    FETCH_ATTEMPTS=3
  fi
  # CURL_RETRY_ARGS is deliberately unquoted: it must word-split into flags.
  fetch_to_file()   { curl -fsSL $CURL_RETRY_ARGS "$1" -o "$2"; }
  fetch_to_stdout() { curl -fsSL $CURL_RETRY_ARGS "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch_to_file()   { wget -qO "$2" "$1"; }
  fetch_to_stdout() { wget -qO- "$1"; }
  FETCH_ATTEMPTS=2
else
  printf 'vc: neither curl nor wget found — install one and re-run.\n' >&2
  exit 1
fi

# Download $1 to $2, retrying the whole fetch FETCH_ATTEMPTS times.
# A failed attempt leaves a truncated file, never a partial one.
fetch_to_file_retry() {
  _fr_url="$1"
  _fr_dest="$2"
  _fr_n=1
  while :; do
    if fetch_to_file "$_fr_url" "$_fr_dest"; then
      return 0
    fi
    : > "$_fr_dest" 2>/dev/null || true
    if [ "$_fr_n" -ge "$FETCH_ATTEMPTS" ]; then
      return 1
    fi
    printf 'vc: download attempt %s/%s failed for %s — retrying\n' \
      "$_fr_n" "$FETCH_ATTEMPTS" "$_fr_url" >&2
    _fr_n=$((_fr_n + 1))
    sleep 1
  done
}

# Print the sha256 of $1, or fail if the machine has no way to compute one.
sha256_of() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" 2>/dev/null | awk '{print $1; exit}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" 2>/dev/null | awk '{print $1; exit}'
  elif command -v openssl >/dev/null 2>&1; then
    openssl dgst -sha256 "$1" 2>/dev/null | awk '{print $NF; exit}'
  else
    return 1
  fi
}

# Verify file $1 against the SHA256SUMS list at URL $2, entry named $3.
# The mirror is a third party: no list, no entry, no tool, or no match all mean
# refuse — installing unverified bytes is worse than not installing.
verify_sha256() {
  _vs_file="$1"
  _vs_url="$2"
  _vs_name="$3"

  _vs_sums="$(mktemp)"
  if ! fetch_to_file_retry "$_vs_url" "$_vs_sums"; then
    printf 'vc: could not fetch %s — refusing to install unverified bytes\n' "$_vs_url" >&2
    rm -f "$_vs_sums"
    return 1
  fi
  _vs_want="$(awk -v n="$_vs_name" '$2 == n || $2 == "*" n { print $1; exit }' "$_vs_sums")" || _vs_want=""
  rm -f "$_vs_sums"
  if [ -z "$_vs_want" ]; then
    printf 'vc: %s lists no sha256 for %s — refusing to install\n' "$_vs_url" "$_vs_name" >&2
    return 1
  fi

  _vs_got="$(sha256_of "$_vs_file")" || _vs_got=""
  if [ -z "$_vs_got" ]; then
    printf 'vc: no sha256 tool (sha256sum / shasum / openssl) — cannot verify the\n' >&2
    printf '    mirror download, refusing to install.\n' >&2
    return 1
  fi
  if [ "$_vs_got" != "$_vs_want" ]; then
    printf 'vc: sha256 mismatch for %s\n' "$_vs_name" >&2
    printf '    expected %s\n' "$_vs_want" >&2
    printf '    got      %s\n' "$_vs_got" >&2
    printf '    Refusing to install. Nothing was replaced.\n' >&2
    return 1
  fi
  printf '==> sha256 verified against %s\n' "$_vs_url" >&2
  return 0
}

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

#
# version.json is fetched with the SAME whole-process retry as everything else.
# It carries the release tag the mirror is addressed by, so a single-shot fetch
# here killed the fallback in precisely the case the fallback exists for: the
# primary host flapping or down. Retried into a temp file, never left behind.
_vj_raw=""
_vj_tmp="$(mktemp)"
if fetch_to_file_retry "$VERSION_JSON_URL" "$_vj_tmp"; then
  _vj_raw="$(cat "$_vj_tmp" 2>/dev/null)" || _vj_raw=""
fi
rm -f "$_vj_tmp"
if [ -n "$_vj_raw" ]; then
  _ver="$(printf '%s' "$_vj_raw" | grep -o '"version"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | grep -o '"[^"]*"$' | tr -d '"')" || true
  if [ -n "$_ver" ]; then
    VERSION_BANNER="==> void-code installer (v${_ver})"
  fi
  # Release tag — the mirror is addressed by tag, not by version.
  _vj_tag="$(printf '%s' "$_vj_raw" | grep -o '"tag"[[:space:]]*:[[:space:]]*"[^"]*"' | head -1 | grep -o '"[^"]*"$' | tr -d '"')" || true
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

# ── GitHub release mirror (fallback source) ──────────────────────────────────
# makscee/void-code publishes the same bytes as a public release. It is used
# only when $AUTH_HOST fails to deliver, and only after its sha256 is checked
# against the SHA256SUMS of the same release — it is a third-party source.
# Needs the tag: no tag in version.json (and no version to derive one from)
# means no mirror, and the primary failure stays fatal.
MIRROR_REPO="${VC_MIRROR_REPO:-makscee/void-code}"
MIRROR_TAG="${_vj_tag:-}"
if [ -z "$MIRROR_TAG" ] && [ -n "${_ver:-}" ]; then
  MIRROR_TAG="v${_ver}"
fi
MIRROR_ASSET="${VC_ARTIFACT_PATH##*/}"
MIRROR_BIN_URL=""
MIRROR_SUMS_URL=""
MIRROR_LATEST_TRIED=0

mirror_urls_from_tag() {
  if [ -n "$MIRROR_TAG" ]; then
    MIRROR_BIN_URL="https://github.com/$MIRROR_REPO/releases/download/$MIRROR_TAG/$MIRROR_ASSET"
    MIRROR_SUMS_URL="https://github.com/$MIRROR_REPO/releases/download/$MIRROR_TAG/SHA256SUMS"
  fi
}

# Last resort for the tag: ask GitHub which release is the latest one. Without
# this the mirror is addressed only by a tag that comes from the very host the
# mirror exists to route around — host fully down means no tag, no mirror URL,
# and a fallback branch that is dead before it is reached.
#
# The REST endpoint is used rather than the redirect from the web release page:
# tag_name is a documented, stable field, the HTML around it is not, and both
# curl and wget read it the same way, with no -w/%{url_effective} equivalent
# needed for the wget path.
#
# Called ONLY after the primary source has already failed to deliver: while
# $AUTH_HOST is healthy this issues no GitHub request at all. A tag resolved
# this way gets NO sha256 exemption — the bytes go through the same
# MIRROR_SUMS_URL check as a tag that came from version.json.
resolve_mirror_tag_from_latest() {
  if [ -n "$MIRROR_TAG" ]; then
    return 0
  fi
  if [ "$MIRROR_LATEST_TRIED" = 1 ]; then
    return 1
  fi
  MIRROR_LATEST_TRIED=1

  printf 'vc: no release tag from %s — asking GitHub for the latest release of %s\n' \
    "$VERSION_JSON_URL" "$MIRROR_REPO" >&2
  _rl_url="https://api.github.com/repos/$MIRROR_REPO/releases/latest"
  _rl_tmp="$(mktemp)"
  if ! fetch_to_file_retry "$_rl_url" "$_rl_tmp"; then
    rm -f "$_rl_tmp"
    printf 'vc: %s did not answer either — no mirror to fall back to.\n' "$_rl_url" >&2
    return 1
  fi
  _rl_tag="$(grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' "$_rl_tmp" | head -1 | grep -o '"[^"]*"$' | tr -d '"')" || _rl_tag=""
  rm -f "$_rl_tmp"
  if [ -z "$_rl_tag" ]; then
    printf 'vc: %s named no tag — no mirror to fall back to.\n' "$_rl_url" >&2
    return 1
  fi

  MIRROR_TAG="$_rl_tag"
  printf '==> using %s, taken from the latest release of %s (releases/latest)\n' \
    "$MIRROR_TAG" "$MIRROR_REPO" >&2
  mirror_urls_from_tag
  return 0
}

mirror_urls_from_tag

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
print_linux_apt_node_dry_run() {
  _sudo_label=""
  [ "$(id -u)" -ne 0 ] && _sudo_label="sudo "
  printf 'WOULD: %sapt-get update\n' "$_sudo_label"
  printf 'WOULD: %sapt-get install -y ca-certificates curl gnupg\n' "$_sudo_label"
  printf 'WOULD: install NodeSource GPG key to /etc/apt/keyrings/nodesource.gpg\n'
  printf 'WOULD: write NodeSource apt source for node_22.x to /etc/apt/sources.list.d/nodesource.list\n'
  printf 'WOULD: %sapt-get update\n' "$_sudo_label"
  printf 'WOULD: %sapt-get install -y nodejs\n' "$_sudo_label"
}

install_node_linux_apt() {
  if ! command -v apt-get >/dev/null 2>&1; then
    printf 'vc: apt-get not found. Install Node.js >= %s via NodeSource or nodejs.org, then re-run.\n' "$MIN_NODE_MAJOR" >&2
    return 1
  fi

  _sudo=""
  if [ "$(id -u)" -ne 0 ]; then
    if command -v sudo >/dev/null 2>&1; then
      _sudo="sudo"
    else
      printf 'vc: apt-get is available, but this user is not root and sudo is missing.\n' >&2
      printf '    Install Node.js >= %s via NodeSource or nodejs.org, then re-run.\n' "$MIN_NODE_MAJOR" >&2
      return 1
    fi
  fi

  if [ "$DRY_RUN" = 1 ]; then
    print_linux_apt_node_dry_run
    return 0
  fi

  printf 'vc: installing Node.js %s.x via NodeSource apt repository...\n' "$MIN_NODE_MAJOR" >&2
  $_sudo apt-get update >&2 || {
    printf 'vc: apt-get update failed. Install Node.js >= %s via NodeSource or nodejs.org, then re-run.\n' "$MIN_NODE_MAJOR" >&2
    return 1
  }
  $_sudo apt-get install -y ca-certificates curl gnupg >&2 || {
    printf 'vc: failed to install apt prerequisites for NodeSource.\n' >&2
    return 1
  }

  _key_tmp="$(mktemp /tmp/nodesource-key-XXXXXX)"
  _keyring_tmp="$(mktemp /tmp/nodesource-keyring-XXXXXX)"
  _list_tmp="$(mktemp /tmp/nodesource-list-XXXXXX)"
  rm -f "$_keyring_tmp"

  fetch_to_file 'https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key' "$_key_tmp" || {
    printf 'vc: failed to download NodeSource signing key.\n' >&2
    rm -f "$_key_tmp" "$_keyring_tmp" "$_list_tmp"
    return 1
  }
  gpg --dearmor -o "$_keyring_tmp" "$_key_tmp" >&2 || {
    printf 'vc: failed to prepare NodeSource signing key.\n' >&2
    rm -f "$_key_tmp" "$_keyring_tmp" "$_list_tmp"
    return 1
  }
  printf 'deb [signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_%s.x nodistro main\n' "$MIN_NODE_MAJOR" > "$_list_tmp"

  $_sudo install -d -m 0755 /etc/apt/keyrings >&2 \
    && $_sudo install -m 0644 "$_keyring_tmp" /etc/apt/keyrings/nodesource.gpg >&2 \
    && $_sudo install -m 0644 "$_list_tmp" /etc/apt/sources.list.d/nodesource.list >&2 || {
      printf 'vc: failed to install NodeSource apt repository files.\n' >&2
      rm -f "$_key_tmp" "$_keyring_tmp" "$_list_tmp"
      return 1
    }
  rm -f "$_key_tmp" "$_keyring_tmp" "$_list_tmp"

  $_sudo apt-get update >&2 || {
    printf 'vc: apt-get update after adding NodeSource failed.\n' >&2
    return 1
  }
  $_sudo apt-get install -y nodejs >&2 || {
    printf 'vc: apt-get install nodejs failed. Install Node.js >= %s via NodeSource or nodejs.org, then re-run.\n' "$MIN_NODE_MAJOR" >&2
    return 1
  }
}

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
  elif [ "$OS" = "linux" ]; then
    install_node_linux_apt || return 1
  elif [ "$OS" = "darwin" ]; then
    # No Homebrew on macOS — download the official Apple-notarized Node LTS .pkg
    # from nodejs.org. The macOS .pkg is UNIVERSAL (one file, both arches) — named
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
  else
    printf 'vc: unsupported OS for automatic Node.js bootstrap. Install Node.js >= %s via nodejs.org, then re-run.\n' "$MIN_NODE_MAJOR" >&2
    return 1
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

NPM_INSTALL_RETRY_ARGS="--maxsockets=1 --fetch-retries=5 --fetch-retry-mintimeout=20000 --fetch-retry-maxtimeout=120000 --fetch-timeout=300000"
NPM_NODE_OPTIONS="--dns-result-order=ipv4first"

npm_install_managed_pi() {
  _attempt=1
  while [ "$_attempt" -le 3 ]; do
    if NODE_OPTIONS="$NPM_NODE_OPTIONS" npm install --prefix "$PI_RUNTIME_DIR" $NPM_INSTALL_RETRY_ARGS --no-save @earendil-works/pi-coding-agent && [ -x "$PI_ENTRY" ]; then
      return 0
    fi
    [ "$_attempt" -ge 3 ] && return 1
    sleep 5
    _attempt=$((_attempt + 1))
  done
}

npm_install_global() {
  _pkg="$1"
  _attempt=1
  _node_options="$NPM_NODE_OPTIONS"
  if [ -n "${NODE_OPTIONS:-}" ]; then
    case " $NODE_OPTIONS " in
      *" $NPM_NODE_OPTIONS "*) _node_options="$NODE_OPTIONS" ;;
      *) _node_options="$NODE_OPTIONS $NPM_NODE_OPTIONS" ;;
    esac
  fi
  while [ "$_attempt" -le 3 ]; do
    if NODE_OPTIONS="$_node_options" npm install -g $NPM_INSTALL_RETRY_ARGS "$_pkg"; then
      return 0
    fi

    if [ "$_attempt" -ge 3 ]; then
      return 1
    fi

    _next_attempt=$((_attempt + 1))
    case "$_attempt" in
      1) _delay=5 ;;
      *) _delay=10 ;;
    esac
    printf 'vc: npm install failed; retrying attempt %s/3 in %ss...\n' "$_next_attempt" "$_delay" >&2
    sleep "$_delay"
    _attempt="$_next_attempt"
  done
  return 1
}

print_npm_install_global() {
  printf 'NODE_OPTIONS=%s npm install -g %s %s' "$NPM_NODE_OPTIONS" "$NPM_INSTALL_RETRY_ARGS" "$1"
}

agent_bin_present() {
  _bin="$1"
  if [ "$_bin" = "pi" ]; then
    [ -x "$PI_ENTRY" ]
    return
  fi
  command -v "$_bin" >/dev/null 2>&1 || [ -x "$HOME/.void-code/bin/$_bin" ]
}

codex_command() {
  if command -v codex >/dev/null 2>&1; then
    command -v codex
    return 0
  fi
  if [ -x "$HOME/.void-code/bin/codex" ]; then
    printf '%s\n' "$HOME/.void-code/bin/codex"
    return 0
  fi
  if command -v npm >/dev/null 2>&1; then
    _prefix="$(npm prefix -g 2>/dev/null)" || true
    if [ -n "$_prefix" ] && [ -x "$_prefix/bin/codex" ]; then
      printf '%s\n' "$_prefix/bin/codex"
      return 0
    fi
  fi
  return 1
}

codex_version_output() {
  _codex_cmd="$(codex_command)" || return 1
  [ -n "$_codex_cmd" ] || return 1
  "$_codex_cmd" --version 2>&1
}

codex_health_check() {
  _out="$(codex_version_output)" || return 1
  case "$_out" in
    *codex-cli*) return 0 ;;
    *)           return 1 ;;
  esac
}

codex_native_platform() {
  case "$OS/$ARCH" in
    linux/amd64)  printf 'linux-x64' ;;
    linux/arm64)  printf 'linux-arm64' ;;
    darwin/amd64) printf 'darwin-x64' ;;
    darwin/arm64) printf 'darwin-arm64' ;;
    *)            printf '' ;;
  esac
}

codex_installed_version() {
  _root="$(npm root -g 2>/dev/null)" || true
  if [ -n "$_root" ] && [ -f "$_root/@openai/codex/package.json" ]; then
    _ver="$(grep -o '"version"[[:space:]]*:[[:space:]]*"[^"]*"' "$_root/@openai/codex/package.json" | head -1 | grep -o '"[^"]*"$' | tr -d '"')" || true
    if [ -n "$_ver" ]; then
      printf '%s' "$_ver"
      return 0
    fi
  fi

  _line="$(npm list -g @openai/codex --depth=0 2>/dev/null | grep '@openai/codex@' | head -1)" || true
  _ver="$(printf '%s' "$_line" | sed 's/^.*@openai\/codex@//; s/[[:space:]].*$//')" || true
  if [ -n "$_ver" ] && [ "$_ver" != "$_line" ]; then
    printf '%s' "$_ver"
    return 0
  fi

  return 1
}

codex_native_optional_missing() {
  _out="$(codex_version_output)" || true
  case "$_out" in
    *"Missing optional dependency"*|*"missing optional dependency"*) return 0 ;;
    *) return 1 ;;
  esac
}

repair_codex_native_optional() {
  codex_native_optional_missing || return 1

  if ! command -v npm >/dev/null 2>&1; then
    printf 'vc: codex native repair needs npm, but npm is not available.\n' >&2
    return 1
  fi

  _platform="$(codex_native_platform)"
  if [ -z "$_platform" ]; then
    printf 'vc: codex native repair is not available for %s/%s.\n' "$OS" "$ARCH" >&2
    return 1
  fi

  _version="$(codex_installed_version)" || true
  if [ -z "$_version" ]; then
    printf 'vc: codex native repair could not determine installed @openai/codex version.\n' >&2
    return 1
  fi

  _native_pkg="@openai/codex-$_platform@npm:@openai/codex@$_version-$_platform"
  printf 'vc: codex wrapper found but native optional package is missing; repairing %s...\n' "$_native_pkg" >&2
  if npm_install_global "$_native_pkg" >&2; then
    if codex_health_check; then
      printf 'vc: codex native package repaired.\n' >&2
      return 0
    fi
    printf 'vc: codex native repair ran, but codex --version still did not report codex-cli.\n' >&2
    return 1
  fi

  printf 'vc: codex native package repair failed.\n' >&2
  return 1
}

agent_health_check() {
  _bin="$1"
  if [ "$_bin" = "codex" ]; then
    codex_health_check
  else
    agent_bin_present "$_bin"
  fi
}

# Check an npm-installed agent binary; install selected packages deterministically.
# Returns 0 if present (or successfully installed), 1 if still absent.
check_npm_agent() {
  _bin="$1"
  _pkg="$2"
  _label="$3"

  if agent_health_check "$_bin"; then
    printf 'vc: %s already installed\n' "$_bin" >&2
    return 0
  fi

  if [ "$_bin" = "codex" ] && codex_command >/dev/null 2>&1; then
    repair_codex_native_optional && return 0
    printf 'vc: codex found, but codex --version did not report codex-cli.\n' >&2
  fi

  if ! command -v npm >/dev/null 2>&1; then
    printf '\nvc: %s not found, and npm is not available.\n' "$_bin" >&2
    printf '    Install Node.js first (see above), then re-run.\n' >&2
    return 1
  fi

  printf '\nvc: %s (%s) is not installed or healthy.\n' "$_pkg" "$_label" >&2

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
      # Non-interactive selected installs are deterministic: no prompt, install.
      _do_install=1
    fi
  fi

  if [ "$_do_install" = 1 ]; then
    printf 'vc: installing %s via npm...\n' "$_pkg" >&2
    if { [ "$_bin" = "pi" ] && npm_install_managed_pi || [ "$_bin" != "pi" ] && npm_install_global "$_pkg"; } >&2; then
      if [ "$_bin" = "codex" ]; then
        if codex_health_check || repair_codex_native_optional; then
          printf 'vc: %s installed.\n' "$_pkg" >&2
          printf '    Note: open a new terminal before running vc (npm PATH update).\n' >&2
          return 0
        fi
        printf 'vc: %s installed, but codex --version did not report codex-cli.\n' "$_pkg" >&2
        return 1
      fi
      printf 'vc: %s installed.\n' "$_pkg" >&2
      printf '    Note: open a new terminal before running vc (npm PATH update).\n' >&2
      return 0
    else
      printf 'vc: npm install failed.\n' >&2
      printf '    Run manually: ' >&2
      if [ "$_bin" = "pi" ]; then
        printf 'npm install --prefix %s %s --no-save @earendil-works/pi-coding-agent' "$PI_RUNTIME_DIR" "$NPM_INSTALL_RETRY_ARGS" >&2
      else
        print_npm_install_global "$_pkg" >&2
      fi
      printf '\n' >&2
      return 1
    fi
  else
    printf '    Run when ready: ' >&2
    if [ "$_bin" = "pi" ]; then
      printf 'npm install --prefix %s %s --no-save @earendil-works/pi-coding-agent' "$PI_RUNTIME_DIR" "$NPM_INSTALL_RETRY_ARGS" >&2
    else
      print_npm_install_global "$_pkg" >&2
    fi
    printf '\n' >&2
    return 1
  fi
}

check_selected_agents() {
  [ "$INSTALL_PI" = 1 ] && check_npm_agent pi @earendil-works/pi-coding-agent Pi || true
  [ "$INSTALL_CLAUDE" = 1 ] && check_npm_agent claude @anthropic-ai/claude-code "Claude Code" || true
  [ "$INSTALL_CODEX" = 1 ] && check_npm_agent codex @openai/codex "OpenAI Codex" || true
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
  if [ -n "$MIRROR_BIN_URL" ]; then
    printf 'FALLBACK (only if the GET above fails): GET %s\n' "$MIRROR_BIN_URL"
    printf '  verified against %s before install\n' "$MIRROR_SUMS_URL"
  else
    printf 'FALLBACK: version.json carried no release tag — at install time the tag\n'
    printf '  would be resolved from https://api.github.com/repos/%s/releases/latest\n' "$MIRROR_REPO"
    printf '  and the download verified against the SHA256SUMS of that release\n'
  fi
  if [ "$(uname -s)" = "Darwin" ]; then
    printf 'WOULD: security add-trusted-cert -r trustRoot -k ~/Library/Keychains/login.keychain-db <relay-ca.pem>\n'
  else
    printf 'WOULD: install relay-ca.pem to system anchors + update-ca-certificates / update-ca-trust\n'
  fi
  _dry_major="$(node_major)"
  if [ -z "$_dry_major" ] || ! [ "$_dry_major" -ge "$MIN_NODE_MAJOR" ] 2>/dev/null; then
    if command -v brew >/dev/null 2>&1; then
      printf 'WOULD: brew install node\n'
    elif [ "$OS" = "linux" ]; then
      print_linux_apt_node_dry_run
    elif [ "$OS" = "darwin" ]; then
      printf 'WOULD: download https://nodejs.org/dist/latest-v22.x/node-v<latest>.pkg  (universal)\n'
      printf 'WOULD: sudo installer -pkg <node.pkg> -target /\n'
    else
      printf 'MANUAL: install Node.js >= %s from https://nodejs.org, then re-run\n' "$MIN_NODE_MAJOR"
    fi
  fi
  if [ "$INSTALL_PI" = 1 ]; then
    printf 'WOULD: npm install --prefix %s %s --no-save @earendil-works/pi-coding-agent\n' "$PI_RUNTIME_DIR" "$NPM_INSTALL_RETRY_ARGS"
  fi
  if [ "$INSTALL_CLAUDE" = 1 ]; then
    printf 'WOULD: '
    print_npm_install_global @anthropic-ai/claude-code
    printf '\n'
  fi
  if [ "$INSTALL_CODEX" = 1 ]; then
    printf 'WOULD: '
    print_npm_install_global @openai/codex
    printf '\n'
  fi
  _dry_rc="$(detect_rc_file)"
  printf 'RC file: %s\n' "${_dry_rc}"
  printf 'NEXT: vc login\n'
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
  VC_BIN_SOURCE=""
  if fetch_to_file_retry "$VC_BIN_URL" "$TMP_BIN"; then
    VC_BIN_SOURCE="$VC_BIN_URL"
  else
    printf 'vc: %s did not deliver — looking for a public release mirror\n' "$VC_BIN_URL" >&2
    # Only here, with the primary already proven dead, may the tag be resolved
    # from GitHub. A no-op when version.json already supplied one.
    resolve_mirror_tag_from_latest || true
    if [ -n "$MIRROR_BIN_URL" ]; then
      printf '==> downloading %s\n' "$MIRROR_BIN_URL" >&2
      if fetch_to_file_retry "$MIRROR_BIN_URL" "$TMP_BIN"; then
        if verify_sha256 "$TMP_BIN" "$MIRROR_SUMS_URL" "$MIRROR_ASSET"; then
          VC_BIN_SOURCE="$MIRROR_BIN_URL"
        else
          rm -f "$TMP_BIN"
          exit 1
        fi
      fi
    fi
  fi
  if [ -z "$VC_BIN_SOURCE" ]; then
    printf 'vc: failed to download %s\n' "$VC_BIN_URL" >&2
    if [ -n "$MIRROR_BIN_URL" ]; then
      printf 'vc: the mirror %s failed too.\n' "$MIRROR_BIN_URL" >&2
    fi
    printf '    Check your connection and re-run; nothing was installed.\n' >&2
    rm -f "$TMP_BIN"
    exit 1
  fi

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
  printf '==> installed to %s (from %s)\n' "$BIN_DIR/vc" "$VC_BIN_SOURCE" >&2
fi

# 2. Provision the production relay CA (public cert only)
#
# Fetched to a temp file and moved into place only once it is whole. It used to
# be written straight to its final path, and fetch_to_file_retry truncates its
# destination when it runs out of attempts — so a re-run on a flapping network
# blanked a relay-ca.pem that was working a minute ago and left the machine
# worse than before the installer touched it.
printf '==> provisioning relay CA\n' >&2
CA_FILE="$CA_DIR/relay-ca.pem"
CA_TMP="$(mktemp)"
if fetch_to_file_retry "$RELAY_CA_URL" "$CA_TMP"; then
  chmod 0644 "$CA_TMP"
  mv -f "$CA_TMP" "$CA_FILE"
else
  rm -f "$CA_TMP"
  if [ -s "$CA_FILE" ]; then
    # A usable cert is already on the machine and stays exactly as it was. The
    # install is complete without a fresh copy, so this is loud, not fatal.
    printf 'vc: failed to download relay CA from %s — kept the existing %s untouched.\n' \
      "$RELAY_CA_URL" "$CA_FILE" >&2
    printf '    Re-run the installer once the network is back to refresh it.\n' >&2
  else
    # Nothing to keep, so leave nothing behind: an empty relay-ca.pem is a file
    # vc will read and choke on, and it passes every test for existence.
    rm -f "$CA_FILE"
    printf 'vc: failed to download relay CA from %s\n' "$RELAY_CA_URL" >&2
    exit 1
  fi
fi

# 2b. Trust the relay CA in the OS store so curl/git (not just node) work in-session.
trust_relay_ca

# 3. PATH — register vc FIRST, before the node/agent bootstrap. The vc launcher
# (vc login, vc doctor) works without node; a node/agent hiccup must NEVER leave
# vc off PATH. Previously this ran AFTER node bootstrap, so a node failure's
# `exit 1` skipped it entirely → vc installed but unreachable on fresh machines.
if [ "${VC_SKIP_DOWNLOAD:-0}" != "1" ]; then
  RC_FILE="$(detect_rc_file)"
  append_path_to_rc "$RC_FILE" "$BIN_DIR"
fi

# 4. node/npm + selected agent bootstrap — NON-FATAL. On failure, vc is already
# installed and on PATH; the post-install steps print exactly what's left.
printf '==> bootstrapping node / selected agents\n' >&2
NODE_OK=0
if ensure_node; then
  NODE_OK=1
  check_selected_agents
else
  printf 'vc: node bootstrap incomplete — vc itself is installed; finish node + selected agents per the steps below.\n' >&2
fi

# ── post-install UX ───────────────────────────────────────────────────────────
if [ "${VC_SKIP_DOWNLOAD:-0}" != "1" ]; then
  _pi_ok=0
  [ -x "$PI_ENTRY" ] && _pi_ok=1
  _claude_ok=0
  command -v claude >/dev/null 2>&1 && _claude_ok=1
  [ -x "$HOME/.void-code/bin/claude" ] && _claude_ok=1
  _codex_ok=0
  codex_health_check && _codex_ok=1

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
    printf '\n  %s. Install Node.js >= %s (required for selected agent CLIs):\n' "$_n" "$MIN_NODE_MAJOR"
    if [ "$OS" = "linux" ]; then
      printf '         Use NodeSource for Debian/Ubuntu or download from https://nodejs.org, then re-run.\n'
    elif [ "$OS" = "darwin" ]; then
      printf '         Download the macOS installer from https://nodejs.org and run it.\n'
    else
      printf '         Download from https://nodejs.org, then re-run.\n'
    fi
    _n=$((_n + 1))
  fi
  _agents_missing=0
  if [ "$INSTALL_PI" = 1 ] && [ "$_pi_ok" = 0 ]; then
    printf '\n  %s. Install Pi:\n' "$_n"
    printf '         npm install --prefix %s %s --no-save @earendil-works/pi-coding-agent\n' "$PI_RUNTIME_DIR" "$NPM_INSTALL_RETRY_ARGS"
    _agents_missing=1
    _n=$((_n + 1))
  fi
  if [ "$INSTALL_CLAUDE" = 1 ] && [ "$_claude_ok" = 0 ]; then
    printf '\n  %s. Install Claude Code:\n' "$_n"
    printf '         '
    print_npm_install_global @anthropic-ai/claude-code
    printf '\n'
    _agents_missing=1
    _n=$((_n + 1))
  fi
  if [ "$INSTALL_CODEX" = 1 ] && [ "$_codex_ok" = 0 ]; then
    printf '\n  %s. Install OpenAI Codex:\n' "$_n"
    printf '         '
    print_npm_install_global @openai/codex
    printf '\n'
    _agents_missing=1
    _n=$((_n + 1))
  fi
  if [ "$_vc_ok" = 0 ] || [ "$NODE_OK" = 0 ] || [ "$_agents_missing" = 1 ]; then
    printf '\n  %s. Open a NEW terminal (picks up PATH + npm changes)\n' "$_n"
    printf '         or run: source %s\n' "${RC_FILE:-~/.zshrc}"
    _n=$((_n + 1))
  fi
  printf '\n  %s. Log in interactively: vc login\n' "$_n"
  _n=$((_n + 1))
  printf '\n  %s. Run: vc\n' "$_n"

  printf '\n'
  printf '  Stuck? Run: vc doctor\n'
  printf '\n'
fi

# shellcheck shell=bash
# Shared helpers for AXON's macOS install/uninstall scripts.
# Sourced, never executed directly. Provides logging in the same ✓/↻/⚠/✗
# vocabulary as `axon init`, plus a few small utilities.

# Colors only when stdout is a TTY (keeps logs/pipes clean).
if [ -t 1 ]; then
  _C_RESET=$'\033[0m'; _C_DIM=$'\033[2m'; _C_BOLD=$'\033[1m'
  _C_GREEN=$'\033[32m'; _C_YELLOW=$'\033[33m'; _C_RED=$'\033[31m'; _C_BLUE=$'\033[34m'
else
  _C_RESET=; _C_DIM=; _C_BOLD=; _C_GREEN=; _C_YELLOW=; _C_RED=; _C_BLUE=
fi

step() { printf '\n%s==>%s %s%s%s\n' "$_C_BLUE" "$_C_RESET" "$_C_BOLD" "$*" "$_C_RESET"; }
ok()   { printf '  %s✓%s %s\n'  "$_C_GREEN"  "$_C_RESET" "$*"; }
skip() { printf '  %s↻%s %s\n'  "$_C_DIM"    "$_C_RESET" "$*"; }
warn() { printf '  %s⚠%s %s\n'  "$_C_YELLOW" "$_C_RESET" "$*"; }
info() { printf '    %s%s%s\n'  "$_C_DIM"    "$*" "$_C_RESET"; }
err()  { printf '  %s✗%s %s\n'  "$_C_RED"    "$_C_RESET" "$*" >&2; }
die()  { err "$*"; exit 1; }

# have CMD — true if CMD is on PATH.
have() { command -v "$1" >/dev/null 2>&1; }

# confirm PROMPT — ask a yes/no question (default yes). Non-interactive => yes.
confirm() {
  [ -t 0 ] || return 0
  local reply
  printf '  %s? %s [Y/n] ' "$_C_YELLOW" "$_C_RESET$*"
  read -r reply || true
  case "$reply" in [nN]*) return 1 ;; *) return 0 ;; esac
}

# run_priv DEST_DIR -- CMD... — run a command, prefixing sudo only when DEST_DIR
# is not writable by the current user. Keeps the install free of needless sudo.
run_priv() {
  local dir="$1"; shift
  [ "$1" = "--" ] && shift
  if [ -w "$dir" ] || { [ ! -e "$dir" ] && [ -w "$(dirname "$dir")" ]; }; then
    "$@"
  else
    warn "needs elevated permission to write $dir"
    sudo "$@"
  fi
}

# require_macos — abort on non-Darwin hosts.
require_macos() {
  [ "$(uname -s)" = "Darwin" ] || die "this script targets macOS; on Linux use 'make install' then 'axon init' + 'axon service install' (systemd)."
}

# ── OS + package-manager awareness ──────────────────────────────────────────

# axon_os — normalised OS name: macos | linux | windows | <uname>.
axon_os() {
  case "$(uname -s)" in
    Darwin) echo macos ;;
    Linux)  echo linux ;;
    MINGW*|MSYS*|CYGWIN*) echo windows ;;
    *) uname -s | tr '[:upper:]' '[:lower:]' ;;
  esac
}

# axon_arch — normalised CPU arch: arm64 | amd64 | <uname -m>.
axon_arch() {
  case "$(uname -m)" in
    arm64|aarch64) echo arm64 ;;
    x86_64|amd64)  echo amd64 ;;
    *) uname -m ;;
  esac
}

# pkg_manager — the host's primary package manager, or "none".
pkg_manager() {
  if   have brew;    then echo brew
  elif have apt-get; then echo apt
  elif have dnf;     then echo dnf
  elif have pacman;  then echo pacman
  elif have zypper;  then echo zypper
  else echo none; fi
}

# install_hint TOOL — print the OS/pkg-manager-appropriate way to install TOOL.
# Always prints *something* actionable, falling back to an official URL.
install_hint() {
  local tool="$1" pm; pm="$(pkg_manager)"
  case "$tool" in
    go)
      case "$pm" in
        brew) echo "brew install go" ;;
        apt)  echo "sudo apt-get install -y golang-go   # or https://go.dev/dl for 1.22+" ;;
        dnf)  echo "sudo dnf install -y golang" ;;
        pacman) echo "sudo pacman -S go" ;;
        zypper) echo "sudo zypper install -y go" ;;
        *)    echo "https://go.dev/dl  (need Go 1.22+)" ;;
      esac ;;
    node|npm)
      case "$pm" in
        brew) echo "brew install node" ;;
        apt)  echo "sudo apt-get install -y nodejs npm" ;;
        dnf)  echo "sudo dnf install -y nodejs npm" ;;
        pacman) echo "sudo pacman -S nodejs npm" ;;
        zypper) echo "sudo zypper install -y nodejs npm" ;;
        *)    echo "https://nodejs.org  (LTS)" ;;
      esac ;;
    ollama)
      case "$(axon_os)" in
        macos) [ "$pm" = brew ] && echo "brew install ollama" || echo "https://ollama.com/download" ;;
        linux) echo "curl -fsSL https://ollama.com/install.sh | sh" ;;
        *)     echo "https://ollama.com/download" ;;
      esac ;;
    xcode-clt)
      case "$(axon_os)" in
        macos) echo "xcode-select --install" ;;
        *)     echo "not available on this OS (macOS only)" ;;
      esac ;;
    claude)
      echo "npm install -g @anthropic-ai/claude-code   then: claude login && claude setup-token" ;;
    make)
      case "$pm" in
        brew) echo "xcode-select --install" ;;
        apt)  echo "sudo apt-get install -y build-essential" ;;
        dnf)  echo "sudo dnf groupinstall -y 'Development Tools'" ;;
        pacman) echo "sudo pacman -S base-devel" ;;
        *)    echo "install make + a C toolchain for your OS" ;;
      esac ;;
    git)
      case "$pm" in
        brew) echo "brew install git" ;;
        apt)  echo "sudo apt-get install -y git" ;;
        dnf)  echo "sudo dnf install -y git" ;;
        pacman) echo "sudo pacman -S git" ;;
        *)    echo "https://git-scm.com/downloads" ;;
      esac ;;
    curl)
      case "$pm" in
        brew) echo "brew install curl" ;;
        apt)  echo "sudo apt-get install -y curl" ;;
        dnf)  echo "sudo dnf install -y curl" ;;
        *)    echo "install curl for your OS" ;;
      esac ;;
    *) echo "install '$tool' for your OS" ;;
  esac
}

# ── Version helpers ─────────────────────────────────────────────────────────

# go_version — installed Go version like "1.26.4", or empty if Go is absent.
go_version() { have go && go version 2>/dev/null | awk '{print $3}' | sed 's/^go//' || true; }

# version_ge A B — succeed when dotted-numeric version A >= B (e.g. 1.26.4 1.22).
version_ge() {
  [ "$1" = "$2" ] && return 0
  local IFS=. i
  # shellcheck disable=SC2206
  local a=($1) b=($2)
  for ((i = 0; i < ${#b[@]}; i++)); do
    local x=${a[i]:-0} y=${b[i]:-0}
    x=${x%%[^0-9]*}; y=${y%%[^0-9]*}   # strip suffixes like "-rc1"
    if ((10#${x:-0} > 10#${y:-0})); then return 0; fi
    if ((10#${x:-0} < 10#${y:-0})); then return 1; fi
  done
  return 0
}

# ── Friendly error trap ─────────────────────────────────────────────────────
# Any unhandled failure prints the step context + a recovery hint instead of a
# bare non-zero exit. Call `enable_err_trap` after sourcing; set context with
# `set_ctx "<what we're doing>"` before each risky block.
_AXON_CTX="starting up"
set_ctx() { _AXON_CTX="$*"; }
on_err() {
  local line="${1:-?}" code="${2:-1}"
  printf '\n%s✗ AXON setup failed%s while: %s%s%s\n' "$_C_RED" "$_C_RESET" "$_C_BOLD" "$_AXON_CTX" "$_C_RESET" >&2
  info "location: line $line, exit code $code"
  info "the steps are idempotent — fixing the cause and re-running resumes safely"
  info "run 'make doctor' to check dependencies, or 'axon doctor' once installed"
  exit "$code"
}
enable_err_trap() { trap 'on_err "$LINENO" "$?"' ERR; }

# ── Installation-state detection ────────────────────────────────────────────

# axon_installed_version PREFIX — echo the installed binary's version, or "".
axon_installed_version() {
  local bin="$1/bin/axon"
  [ -x "$bin" ] && "$bin" version --short 2>/dev/null || true
}

# config_missing_keys EXAMPLE CONFIG — list top-level-ish keys present in the
# shipped EXAMPLE but absent from the user's CONFIG, so an update can flag newly
# introduced settings without ever mutating the user's file. Best-effort (a
# simple key scan, not a YAML merge); advisory only.
config_missing_keys() {
  local example="$1" config="$2"
  [ -f "$example" ] && [ -f "$config" ] || return 0
  # Collect "word:" keys (ignoring list items and comments) from both, compare.
  local ex cf
  ex="$(grep -oE '^[[:space:]]{0,4}[a-z_][a-z0-9_]*:' "$example" 2>/dev/null | tr -d ' ' | sort -u)"
  cf="$(grep -oE '^[[:space:]]{0,4}[a-z_][a-z0-9_]*:' "$config"  2>/dev/null | tr -d ' ' | sort -u)"
  comm -23 <(printf '%s\n' "$ex") <(printf '%s\n' "$cf") 2>/dev/null | sed 's/:$//'
}

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
  [ "$(uname -s)" = "Darwin" ] || die "this script targets macOS; on Linux use 'axon init' + 'axon service install' (systemd)."
}

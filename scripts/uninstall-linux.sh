#!/usr/bin/env bash
#
# uninstall-linux.sh — remove what install-linux.sh set up (systemd --user).
#
# Stops and removes the systemd --user unit, removes the binary, and (only with
# --purge) deletes ~/.axon. Your Obsidian vault is NEVER touched.
#
# Usage: scripts/uninstall-linux.sh [options]
#   --prefix DIR     binary location                      (default /usr/local)
#   --profile NAME   profile to remove                    (default: config's active_profile)
#   --purge          also delete ~/.axon (config, secrets, DB, logs) — irreversible
#   -h, --help       show this help

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/_common.sh
. "$SCRIPT_DIR/_common.sh"

PREFIX="${PREFIX:-/usr/local}"
AXON_HOME="${AXON_HOME:-$HOME/.axon}"
PROFILE=""
PURGE=0

usage() { sed -n '3,12p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)  PREFIX="$2"; shift 2 ;;
    --profile) PROFILE="$2"; shift 2 ;;
    --purge)   PURGE=1; shift ;;
    -h|--help) usage 0 ;;
    *) err "unknown option: $1"; usage 1 ;;
  esac
done

[ "$(axon_os)" = linux ] || die "this script targets Linux; on macOS use scripts/uninstall-macos.sh."

BIN="$PREFIX/bin/axon"
CONFIG="$AXON_HOME/config.yaml"
if [ -z "$PROFILE" ] && [ -x "$BIN" ] && [ -f "$CONFIG" ]; then
  PROFILE="$("$BIN" --config "$CONFIG" config get active_profile 2>/dev/null || echo personal)"
fi
PROFILE="${PROFILE:-personal}"
VAULT=""
[ -x "$BIN" ] && [ -f "$CONFIG" ] && VAULT="$("$BIN" --config "$CONFIG" --profile "$PROFILE" config get vault_path 2>/dev/null || true)"
UNIT="axon-$PROFILE.service"

printf '%sAXON Linux uninstaller%s  (profile=%s)\n' "$_C_BOLD" "$_C_RESET" "$PROFILE"

# ── 1. Stop + remove the daemon ─────────────────────────────────────────────
step "Stopping the daemon"
if have systemctl; then
  systemctl --user disable --now "$UNIT" >/dev/null 2>&1 || true
  systemctl --user daemon-reload || true
fi
[ -x "$BIN" ] && "$BIN" --config "$CONFIG" --profile "$PROFILE" stop >/dev/null 2>&1 || true
if [ -x "$BIN" ] && [ -f "$CONFIG" ]; then "$BIN" --config "$CONFIG" --profile "$PROFILE" service uninstall >/dev/null 2>&1 || true; fi
ok "systemd --user unit removed ($UNIT)"

# ── 2. Remove the binary ────────────────────────────────────────────────────
step "Removing binary"
if [ -e "$BIN" ]; then run_priv "$(dirname "$BIN")" -- rm -f "$BIN"; ok "removed $BIN"
else skip "no binary at $BIN"; fi

# ── 3. Data (kept unless --purge) ───────────────────────────────────────────
step "Data directory"
if [ "$PURGE" -eq 1 ]; then
  warn "About to DELETE $AXON_HOME — config, secrets (.env) and the SQLite DB. Irreversible."
  [ -n "$VAULT" ] && info "Your vault ($VAULT) is NOT affected — it's the source of truth and stays put."
  if confirm "Delete $AXON_HOME now?"; then rm -rf "$AXON_HOME"; ok "deleted $AXON_HOME"
  else skip "kept $AXON_HOME"; fi
else
  skip "kept $AXON_HOME (config, secrets, DB). Re-run with --purge to delete it."
fi

step "Done"
ok "AXON daemon + binary removed."
[ -n "$VAULT" ] && ok "Vault preserved: $VAULT"

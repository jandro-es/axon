#!/usr/bin/env bash
#
# uninstall-macos.sh — remove what install-macos.sh set up.
#
# Stops and removes the launchd auto-start agent, removes the binary, and (only
# with --purge) deletes ~/.axon. Your Obsidian vault is NEVER touched.
#
# Usage: scripts/uninstall-macos.sh [options]
#   --prefix DIR     binary location                      (default /usr/local)
#   --profile NAME   profile to remove                    (default: config's active_profile)
#   --purge          also delete ~/.axon (config, secrets, DB, logs) — irreversible
#   --stop-ollama    also stop the Ollama service (off by default; other apps may use it)
#   -h, --help       show this help

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/_common.sh
. "$SCRIPT_DIR/_common.sh"

PREFIX="${PREFIX:-/usr/local}"
AXON_HOME="${AXON_HOME:-$HOME/.axon}"
PROFILE=""
PURGE=0
STOP_OLLAMA=0

usage() { sed -n '3,13p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)     PREFIX="$2"; shift 2 ;;
    --profile)    PROFILE="$2"; shift 2 ;;
    --purge)      PURGE=1; shift ;;
    --stop-ollama) STOP_OLLAMA=1; shift ;;
    -h|--help)    usage 0 ;;
    *) err "unknown option: $1"; usage 1 ;;
  esac
done

require_macos
BIN="$PREFIX/bin/axon"
CONFIG="$AXON_HOME/config.yaml"

# Resolve the profile + vault path (so we can show what we're preserving).
if [ -z "$PROFILE" ] && [ -x "$BIN" ] && [ -f "$CONFIG" ]; then
  PROFILE="$("$BIN" --config "$CONFIG" config get active_profile 2>/dev/null || echo personal)"
fi
PROFILE="${PROFILE:-personal}"
VAULT=""
[ -x "$BIN" ] && [ -f "$CONFIG" ] && VAULT="$("$BIN" --config "$CONFIG" --profile "$PROFILE" config get vault_path 2>/dev/null || true)"
PLIST="$HOME/Library/LaunchAgents/com.axon.$PROFILE.plist"

printf '%sAXON macOS uninstaller%s  (profile=%s)\n' "$_C_BOLD" "$_C_RESET" "$PROFILE"

# ── 1. Stop + remove the daemon ─────────────────────────────────────────────
step "Stopping the daemon"
launchctl stop "com.axon.$PROFILE" >/dev/null 2>&1 || true
launchctl unload "$PLIST"          >/dev/null 2>&1 || true
[ -x "$BIN" ] && "$BIN" --config "$CONFIG" --profile "$PROFILE" stop >/dev/null 2>&1 || true
if [ -x "$BIN" ] && [ -f "$CONFIG" ]; then "$BIN" --config "$CONFIG" --profile "$PROFILE" service uninstall >/dev/null 2>&1 || true; fi
rm -f "$PLIST"
ok "launchd agent removed ($PLIST)"

# ── 2. Ollama (opt-in) ──────────────────────────────────────────────────────
step "Ollama"
if [ "$STOP_OLLAMA" -eq 1 ] && have brew; then
  brew services stop ollama >/dev/null 2>&1 && ok "stopped Ollama service" || warn "could not stop Ollama service"
else
  skip "leaving Ollama running (pass --stop-ollama to stop it; it may serve other apps)"
fi

# ── 3. Remove the binary ────────────────────────────────────────────────────
step "Removing binary"
if [ -e "$BIN" ]; then run_priv "$(dirname "$BIN")" -- rm -f "$BIN"; ok "removed $BIN"
else skip "no binary at $BIN"; fi

# ── 4. Data (kept unless --purge) ───────────────────────────────────────────
step "Data directory"
if [ "$PURGE" -eq 1 ]; then
  warn "About to DELETE $AXON_HOME — config, secrets (.env) and the SQLite DB. This is irreversible."
  [ -n "$VAULT" ] && info "Your vault ($VAULT) is NOT affected — it's the source of truth and stays put."
  if confirm "Delete $AXON_HOME now?"; then rm -rf "$AXON_HOME"; ok "deleted $AXON_HOME"
  else skip "kept $AXON_HOME"; fi
else
  skip "kept $AXON_HOME (config, secrets, DB). Re-run with --purge to delete it."
fi

step "Done"
ok "AXON daemon + binary removed."
[ -n "$VAULT" ] && ok "Vault preserved: $VAULT"
[ "$PURGE" -eq 0 ] && info "Reinstall any time with scripts/install-macos.sh (your config is still in $AXON_HOME)."

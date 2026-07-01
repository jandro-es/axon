#!/usr/bin/env bash
#
# update-macos.sh — update an existing AXON installation in place.
#
# Rebuilds the binary + dashboard from the current source tree, replaces the
# installed binary, converges the profile (`axon init` — DB migrations, vault
# scaffold, Claude Code wiring, dashboards), refreshes the launchd service unit
# and restarts the daemon. Your config, secrets and SQLite DB are preserved; new
# config settings shipped since your install are listed (never applied silently).
#
# Usage: scripts/update-macos.sh [options]
#   --prefix DIR     installed binary location             (default /usr/local)
#   --profile NAME   profile to converge                   (default: config's active_profile)
#   --no-service     don't touch/restart the launchd agent
#   --skip-build     reinstall the existing ./axon build instead of rebuilding
#   -h, --help       show this help
#
# For a FIRST install use scripts/install-macos.sh (or `make setup`).

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(dirname "$SCRIPT_DIR")"
# shellcheck source=scripts/_common.sh
. "$SCRIPT_DIR/_common.sh"
enable_err_trap

PREFIX="${PREFIX:-/usr/local}"
AXON_HOME="${AXON_HOME:-$HOME/.axon}"
PROFILE=""
DO_SERVICE=1
SKIP_BUILD=0

usage() { sed -n '3,17p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)     PREFIX="$2"; shift 2 ;;
    --profile)    PROFILE="$2"; shift 2 ;;
    --no-service) DO_SERVICE=0; shift ;;
    --skip-build) SKIP_BUILD=1; shift ;;
    -h|--help)    usage 0 ;;
    *) err "unknown option: $1"; usage 1 ;;
  esac
done

require_macos

BIN="$PREFIX/bin/axon"
CONFIG="$AXON_HOME/config.yaml"
ENVFILE="$AXON_HOME/.env"
AX=( "$BIN" --config "$CONFIG" --env "$ENVFILE" )
[ -n "$PROFILE" ] && AX+=( --profile "$PROFILE" )

printf '%sAXON updater%s  (prefix=%s, home=%s)\n' "$_C_BOLD" "$_C_RESET" "$PREFIX" "$AXON_HOME"

# ── 0. Confirm this is actually an existing install ─────────────────────────
set_ctx "checking for an existing installation"
if [ ! -x "$BIN" ] || [ ! -f "$CONFIG" ]; then
  err "no existing AXON installation found (looked for $BIN and $CONFIG)"
  info "this is a fresh machine — run the installer instead:"
  info "  make setup        # or: scripts/install-macos.sh"
  exit 1
fi
OLD_VERSION="$(axon_installed_version "$PREFIX")"
ok "found AXON ${OLD_VERSION:-?} at $BIN"

# ── 1. Preflight (build deps only; runtime deps aren't needed to update) ────
set_ctx "checking build dependencies"
bash "$SCRIPT_DIR/preflight.sh" --build -q || die "missing build dependencies (see above) — install them and re-run"

# ── 2. Build the new version ────────────────────────────────────────────────
set_ctx "building the new version"
step "Building axon"
if [ "$SKIP_BUILD" -eq 1 ] && [ -x "$REPO/axon" ]; then
  skip "using existing build $REPO/axon"
else
  if have npm; then
    info "rebuilding dashboard SPA (web/)…"
    if ( cd "$REPO/web" && npm install --silent && npm run build ); then ok "dashboard rebuilt"
    else warn "dashboard SPA build failed — keeping the previous embedded dashboard"; fi
  else
    warn "Node/npm not found — the binary will embed whatever dashboard is in web/dist"
  fi
  info "compiling binary…"
  make -C "$REPO" binary >/dev/null || die "binary build failed — run 'make binary' to see the full error"
  ok "built $REPO/axon"
fi
NEW_VERSION="$("$REPO/axon" version --short 2>/dev/null || echo '?')"

# Validate the CURRENT config against the NEW binary before we swap anything in,
# so a schema change can't leave a broken daemon running the new binary.
set_ctx "validating your config against the new binary"
if "$REPO/axon" --config "$CONFIG" --env "$ENVFILE" config validate >/dev/null 2>&1; then
  ok "config still valid under $NEW_VERSION"
else
  warn "your config isn't valid under the new binary — review it:"
  "$REPO/axon" --config "$CONFIG" --env "$ENVFILE" config validate || true
  confirm "Continue installing anyway?" || die "update aborted; fix $CONFIG and re-run"
fi

# ── 3. Swap in the new binary ───────────────────────────────────────────────
set_ctx "installing the new binary"
step "Installing $NEW_VERSION to $PREFIX/bin"
run_priv "$PREFIX/bin" -- install -d "$PREFIX/bin"
run_priv "$PREFIX/bin" -- install -m 0755 "$REPO/axon" "$BIN"
if [ "$OLD_VERSION" = "$NEW_VERSION" ]; then
  ok "reinstalled $NEW_VERSION (unchanged version)"
else
  ok "updated ${OLD_VERSION:-?} → $NEW_VERSION"
fi

# ── 4. Converge the profile (idempotent; applies DB migrations etc.) ────────
set_ctx "converging the profile (axon init)"
step "Converging profile (axon init)"
PROFILE="${PROFILE:-$("${AX[@]}" config get active_profile 2>/dev/null || echo personal)}"
"${AX[@]}" init   # streams its own ✓/↻/⚠/✗ report: migrations, scaffold, wiring, dashboards

# ── 5. Surface newly shipped config settings (advisory; never auto-applied) ─
set_ctx "checking for new config settings"
NEW_KEYS="$(config_missing_keys "$REPO/axon.config.example.yaml" "$CONFIG")"
if [ -n "$NEW_KEYS" ]; then
  step "New config settings available"
  warn "this release ships config keys your $CONFIG doesn't set yet:"
  while IFS= read -r k; do [ -n "$k" ] && info "• $k"; done <<< "$NEW_KEYS"
  info "compare with $REPO/axon.config.example.yaml and add any you want (optional)."
fi

# ── 6. Refresh + restart the daemon so the new binary/dashboard take effect ─
if [ "$DO_SERVICE" -eq 1 ]; then
  set_ctx "restarting the daemon"
  step "Restarting the daemon"
  PORT="$("${AX[@]}" config get dashboard.port 2>/dev/null || echo 7777)"
  DATADIR="$("${AX[@]}" config get data_dir 2>/dev/null || echo "$AXON_HOME/profiles/$PROFILE")"
  DATADIR="${DATADIR/#\~/$HOME}"
  PLIST="$HOME/Library/LaunchAgents/com.axon.$PROFILE.plist"

  "${AX[@]}" service install >/dev/null   # regenerate the unit in case its format changed
  if [ -f "$PLIST" ]; then
    launchctl unload "$PLIST" >/dev/null 2>&1 || true
    launchctl load -w "$PLIST" && ok "reloaded launchd agent (new binary is now live)"
    info "waiting for the dashboard on :$PORT…"
    for _ in $(seq 1 20); do curl -fsS "http://127.0.0.1:$PORT/" >/dev/null 2>&1 && break; sleep 0.5; done
    curl -fsS "http://127.0.0.1:$PORT/" >/dev/null 2>&1 \
      && ok "daemon up — dashboard at http://127.0.0.1:$PORT" \
      || warn "daemon didn't answer yet — check logs at $DATADIR/logs/daemon.err.log"
  else
    skip "no launchd agent installed — start manually with '${AX[*]} start'"
  fi
else
  skip "left the daemon untouched (--no-service) — restart it yourself to run $NEW_VERSION"
fi

# ── Summary ─────────────────────────────────────────────────────────────────
step "Update complete"
if [ "$OLD_VERSION" = "$NEW_VERSION" ]; then
  ok "AXON reinstalled at $NEW_VERSION (profile '$PROFILE' converged)"
else
  ok "AXON updated ${OLD_VERSION:-?} → $NEW_VERSION (profile '$PROFILE' converged)"
fi
info "verify with: axon doctor   and   axon status"
[ -n "$NEW_KEYS" ] && info "new config settings are listed above — adding them is optional"

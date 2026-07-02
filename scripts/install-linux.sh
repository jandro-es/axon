#!/usr/bin/env bash
#
# install-linux.sh — Linux setup for AXON FROM SOURCE (repo checkout + Go/Node).
#
# Not building from source? The one-liner release install is simpler:
#   curl -fsSL https://raw.githubusercontent.com/jandro-es/axon/main/install.sh | bash
# and later lifecycle is in the binary itself: axon setup / update / uninstall.
#
# Builds the binary (+ dashboard SPA), installs it to PREFIX/bin, scaffolds the
# config + secrets under ~/.axon, runs `axon init`, and installs a systemd --user
# unit so the daemon starts at login. Idempotent and verbose — safe to re-run.
#
# Usage: scripts/install-linux.sh [options]
#   --prefix DIR     install the binary under DIR/bin     (default /usr/local)
#   --profile NAME   provision this profile               (default: config's active_profile)
#   --no-service     don't install the systemd --user unit
#   --skip-build     use an existing ./axon build instead of rebuilding
#   -h, --help       show this help
#
# Update later with scripts/update-linux.sh; remove with scripts/uninstall-linux.sh.

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

[ "$(axon_os)" = linux ] || die "this script targets Linux; on macOS use scripts/install-macos.sh (or 'make setup')."

BIN="$PREFIX/bin/axon"
CONFIG="$AXON_HOME/config.yaml"
ENVFILE="$AXON_HOME/.env"
AX=( "$BIN" --config "$CONFIG" --env "$ENVFILE" )
[ -n "$PROFILE" ] && AX+=( --profile "$PROFILE" )

printf '%sAXON Linux installer%s  (prefix=%s, home=%s)\n' "$_C_BOLD" "$_C_RESET" "$PREFIX" "$AXON_HOME"

# ── 1. Prerequisites ────────────────────────────────────────────────────────
set_ctx "checking prerequisites"
step "Checking prerequisites"
bash "$SCRIPT_DIR/preflight.sh" --all \
  || die "a required dependency is missing (see above) — install it and re-run"
if [ "$DO_SERVICE" -eq 1 ] && ! have systemctl; then
  warn "systemctl not found — will skip auto-start; run the daemon with '${AX[*]} start'"
  DO_SERVICE=0
fi

# ── 2. Build ────────────────────────────────────────────────────────────────
set_ctx "building axon"
step "Building axon"
if [ "$SKIP_BUILD" -eq 1 ] && [ -x "$REPO/axon" ]; then
  skip "using existing build $REPO/axon"
else
  if have npm; then
    info "building dashboard SPA (web/)…"
    if ( cd "$REPO/web" && npm install --silent && npm run build ); then ok "dashboard built"
    else warn "dashboard SPA build failed — the binary serves a fallback page until web/dist exists"; fi
  fi
  info "compiling binary…"
  make -C "$REPO" binary >/dev/null || die "binary build failed — run 'make binary' to see the error"
  ok "built $REPO/axon"
fi

# ── 3. Install the binary ───────────────────────────────────────────────────
set_ctx "installing the binary"
step "Installing binary to $PREFIX/bin"
run_priv "$PREFIX/bin" -- install -d "$PREFIX/bin"
run_priv "$PREFIX/bin" -- install -m 0755 "$REPO/axon" "$BIN"
ok "installed $BIN ($("$BIN" version --short 2>/dev/null || echo '?'))"
case ":$PATH:" in *":$PREFIX/bin:"*) : ;; *) warn "$PREFIX/bin is not on your PATH — add it to your shell profile." ;; esac

# ── 4. Config + secrets ─────────────────────────────────────────────────────
set_ctx "setting up config + secrets"
step "Setting up config + secrets in $AXON_HOME"
mkdir -p "$AXON_HOME"
created_config=0
if [ -f "$CONFIG" ]; then skip "config exists: $CONFIG"
else cp "$REPO/axon.config.example.yaml" "$CONFIG"; created_config=1; ok "created $CONFIG"; fi
if [ -f "$ENVFILE" ]; then skip "secrets exist: $ENVFILE"
else cp "$REPO/.env.example" "$ENVFILE"; chmod 600 "$ENVFILE"; ok "created $ENVFILE (chmod 600)"; fi

if [ "$created_config" -eq 1 ]; then
  warn "Set at least 'vault_path' (your Obsidian vault) in $CONFIG before the daemon is useful."
  if confirm "Open $CONFIG in an editor now?"; then "${EDITOR:-nano}" "$CONFIG"; fi
fi

"${AX[@]}" config validate >/dev/null && ok "config valid" || die "config invalid — fix $CONFIG (see message above) and re-run."

# The apple embeddings provider is macOS-only (NLContextualEmbedding).
EMB_PROVIDER="$("${AX[@]}" config get embeddings.provider 2>/dev/null || echo ollama)"
[ "$EMB_PROVIDER" = apple ] && die "embeddings.provider 'apple' is macOS-only — set it to 'ollama' in $CONFIG"

PROFILE="${PROFILE:-$("${AX[@]}" config get active_profile 2>/dev/null || echo personal)}"
MODEL="$("${AX[@]}" config get embeddings.model 2>/dev/null || echo nomic-embed-text)"
PORT="$("${AX[@]}" config get dashboard.port 2>/dev/null || echo 7777)"

# ── 5. Ollama model (best-effort) ───────────────────────────────────────────
if have ollama; then
  set_ctx "preparing the embedding model"
  step "Ollama"
  if curl -fsS http://localhost:11434/api/tags >/dev/null 2>&1; then
    info "pulling embedding model '$MODEL' (local, one-time)…"; ollama pull "$MODEL" && ok "model '$MODEL' ready" || warn "could not pull '$MODEL' now — do it later with 'ollama pull $MODEL'"
  else
    warn "Ollama not reachable on :11434 — start it ('ollama serve' or 'systemctl --user start ollama'), then 'ollama pull $MODEL'"
  fi
fi

# ── 6. Provision the profile ────────────────────────────────────────────────
set_ctx "provisioning the profile (axon init)"
step "Provisioning profile '$PROFILE' (axon init)"
"${AX[@]}" init

# ── 7. Auto-start at login (systemd --user) ─────────────────────────────────
if [ "$DO_SERVICE" -eq 1 ]; then
  set_ctx "installing the systemd --user unit"
  step "Installing systemd --user service"
  UNIT="axon-$PROFILE.service"
  "${AX[@]}" service install >/dev/null
  systemctl --user daemon-reload || true
  systemctl --user enable --now "$UNIT" && ok "enabled + started $UNIT (starts at login)" \
    || warn "could not enable $UNIT — check 'systemctl --user status $UNIT'"
  info "tip: 'loginctl enable-linger $USER' keeps the daemon running when you're logged out"
  info "waiting for the dashboard on :$PORT…"
  for _ in $(seq 1 20); do curl -fsS "http://127.0.0.1:$PORT/" >/dev/null 2>&1 && break; sleep 0.5; done
  curl -fsS "http://127.0.0.1:$PORT/" >/dev/null 2>&1 \
    && ok "daemon up — dashboard at http://127.0.0.1:$PORT" \
    || warn "daemon didn't answer yet — check 'systemctl --user status $UNIT' and its journal"
else
  skip "auto-start skipped — run the daemon with '${AX[*]} start'"
fi

# ── Summary ─────────────────────────────────────────────────────────────────
step "Done"
ok "binary:  $BIN"
ok "config:  $CONFIG"
ok "profile: $PROFILE"
[ "$DO_SERVICE" -eq 1 ] && ok "dashboard: http://127.0.0.1:$PORT"
cat <<EOF

  Next steps:
    • axon doctor                 # health check (claude/ollama/ports/vault)
    • axon status                 # daemon health + budget + last runs
  Manage the daemon (systemd --user):
    • systemctl --user status  axon-$PROFILE
    • systemctl --user restart axon-$PROFILE
    • systemctl --user disable axon-$PROFILE   # stop auto-start
  Update later:  scripts/update-linux.sh        (or 'make update')
  Uninstall:     scripts/uninstall-linux.sh     (add --purge to delete $AXON_HOME)
EOF

#!/usr/bin/env bash
#
# install-macos.sh — one-command macOS setup for AXON.
#
# Builds the binary (and dashboard SPA), installs it to PREFIX/bin, scaffolds the
# config + secrets under ~/.axon, makes Ollama start at login, runs `axon init`,
# and installs a launchd agent so the AXON daemon starts at login too. Idempotent
# and verbose — safe to re-run after editing your config.
#
# Usage: scripts/install-macos.sh [options]
#   --prefix DIR     install the binary under DIR/bin     (default /usr/local)
#   --profile NAME   provision this profile               (default: config's active_profile)
#   --no-service     don't install the launchd auto-start agent
#   --no-ollama      don't install/manage Ollama
#   --skip-build     use an existing ./axon build instead of rebuilding
#   -h, --help       show this help
#
# Uninstall with: scripts/uninstall-macos.sh

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(dirname "$SCRIPT_DIR")"
# shellcheck source=scripts/_common.sh
. "$SCRIPT_DIR/_common.sh"

PREFIX="${PREFIX:-/usr/local}"
AXON_HOME="${AXON_HOME:-$HOME/.axon}"
PROFILE=""
DO_SERVICE=1
DO_OLLAMA=1
SKIP_BUILD=0

usage() { sed -n '3,18p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)    PREFIX="$2"; shift 2 ;;
    --profile)   PROFILE="$2"; shift 2 ;;
    --no-service) DO_SERVICE=0; shift ;;
    --no-ollama)  DO_OLLAMA=0; shift ;;
    --skip-build) SKIP_BUILD=1; shift ;;
    -h|--help)   usage 0 ;;
    *) err "unknown option: $1"; usage 1 ;;
  esac
done

BIN="$PREFIX/bin/axon"
CONFIG="$AXON_HOME/config.yaml"
ENVFILE="$AXON_HOME/.env"
# axon resolves its own config/env; we pass these so the script is robust to a
# non-default AXON_HOME and so every invocation is explicit.
AX=( "$BIN" --config "$CONFIG" --env "$ENVFILE" )
[ -n "$PROFILE" ] && AX+=( --profile "$PROFILE" )

require_macos
printf '%sAXON macOS installer%s  (prefix=%s, home=%s)\n' "$_C_BOLD" "$_C_RESET" "$PREFIX" "$AXON_HOME"

# ── 1. Prerequisites ────────────────────────────────────────────────────────
step "Checking prerequisites"
have go   || die "Go toolchain not found — install Go 1.22+ (https://go.dev/dl or 'brew install go')."
ok "go $(go version | awk '{print $3}' | sed 's/go//')"

if have node && have npm; then ok "node $(node --version)"
else warn "Node/npm not found — building the binary without the dashboard SPA (a fallback page is served). Install with 'brew install node' for the full dashboard."; fi

have claude && ok "claude CLI present" \
  || warn "claude CLI not found — needed for automations + interactive use. Install Claude Code, then 'claude login' && 'claude setup-token'."

if [ "$DO_OLLAMA" -eq 1 ]; then
  if have ollama; then ok "ollama present"
  elif have brew;  then info "installing Ollama via Homebrew…"; brew install ollama && ok "ollama installed" || warn "Ollama install failed — install it manually (https://ollama.com); not required until embeddings run"
  else warn "ollama not found and Homebrew unavailable — install Ollama manually (https://ollama.com) and re-run."; fi
fi
have brew || warn "Homebrew not found — Ollama auto-start uses 'brew services'. Without it you'll start Ollama yourself."

# ── 2. Build ────────────────────────────────────────────────────────────────
step "Building axon"
if [ "$SKIP_BUILD" -eq 1 ] && [ -x "$REPO/axon" ]; then
  skip "using existing build $REPO/axon"
else
  if have npm; then
    info "building dashboard SPA (web/)…"
    if ( cd "$REPO/web" && npm install --silent && npm run build ); then ok "dashboard built"
    else warn "dashboard SPA build failed — continuing; the binary serves a fallback page until web/dist exists"; fi
  fi
  info "compiling binary…"
  make -C "$REPO" binary >/dev/null || die "binary build failed — run 'make binary' to see the error"
  ok "built $REPO/axon"
fi

# ── 3. Install the binary ───────────────────────────────────────────────────
step "Installing binary to $PREFIX/bin"
run_priv "$PREFIX/bin" -- install -d "$PREFIX/bin"
run_priv "$PREFIX/bin" -- install -m 0755 "$REPO/axon" "$BIN"
ok "installed $BIN ($("$BIN" version 2>/dev/null || echo '?'))"
case ":$PATH:" in *":$PREFIX/bin:"*) : ;; *) warn "$PREFIX/bin is not on your PATH — add it to your shell profile." ;; esac

# ── 4. Config + secrets ─────────────────────────────────────────────────────
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
if grep -q 'sk-ant-oat01-…\|CHANGE_ME\|<' "$ENVFILE" 2>/dev/null; then
  warn "Add your CLAUDE_CODE_OAUTH_TOKEN to $ENVFILE (get it from 'claude setup-token') so headless automations can reach Claude."
fi

# Validate before doing anything that depends on a good config.
"${AX[@]}" config validate >/dev/null && ok "config valid" || die "config invalid — fix $CONFIG (see message above) and re-run."

# Resolve the values we need for service + ollama from the live config.
PROFILE="${PROFILE:-$("${AX[@]}" config get active_profile 2>/dev/null || echo personal)}"
MODEL="$("${AX[@]}" config get embeddings.model 2>/dev/null || echo nomic-embed-text)"
PORT="$("${AX[@]}" config get dashboard.port 2>/dev/null || echo 7777)"
DATADIR="$("${AX[@]}" config get data_dir 2>/dev/null || echo "$AXON_HOME/profiles/$PROFILE")"
DATADIR="${DATADIR/#\~/$HOME}"   # expand a leading ~ for display/log paths
PLIST="$HOME/Library/LaunchAgents/com.axon.$PROFILE.plist"

# ── 5. Ollama at login + embedding model ────────────────────────────────────
if [ "$DO_OLLAMA" -eq 1 ] && have ollama; then
  step "Configuring Ollama"
  if have brew && brew list ollama >/dev/null 2>&1; then
    brew services start ollama >/dev/null 2>&1 && ok "Ollama runs at login (brew services)"
  else
    warn "Ollama isn't Homebrew-managed — enable 'Launch at login' in the Ollama app, or run 'ollama serve' yourself."
    have ollama && pgrep -qf 'ollama serve' || ( ollama serve >/dev/null 2>&1 & ) || true
  fi
  info "waiting for Ollama to answer on :11434…"
  for _ in $(seq 1 20); do curl -fsS http://localhost:11434/api/tags >/dev/null 2>&1 && break; sleep 0.5; done
  if curl -fsS http://localhost:11434/api/tags >/dev/null 2>&1; then
    info "pulling embedding model '$MODEL' (local, one-time)…"; ollama pull "$MODEL" && ok "model '$MODEL' ready"
  else
    warn "Ollama not reachable yet — pull the model later with 'ollama pull $MODEL'."
  fi
fi

# ── 6. Provision the profile ────────────────────────────────────────────────
step "Provisioning profile '$PROFILE' (axon init)"
"${AX[@]}" init   # streams its own ✓/↻/⚠/✗ report

# ── 7. Auto-start the daemon at login (launchd) ─────────────────────────────
if [ "$DO_SERVICE" -eq 1 ]; then
  step "Installing launchd auto-start agent"
  "${AX[@]}" service install >/dev/null
  launchctl unload "$PLIST" >/dev/null 2>&1 || true   # reload cleanly if it already existed
  launchctl load -w "$PLIST" && ok "loaded $PLIST (starts at login, restarts on crash)"
  info "waiting for the dashboard on :$PORT…"
  for _ in $(seq 1 20); do curl -fsS "http://127.0.0.1:$PORT/" >/dev/null 2>&1 && break; sleep 0.5; done
  curl -fsS "http://127.0.0.1:$PORT/" >/dev/null 2>&1 \
    && ok "daemon up — dashboard at http://127.0.0.1:$PORT" \
    || warn "daemon didn't answer yet — check logs at $DATADIR/logs/daemon.err.log"
else
  skip "auto-start skipped (--no-service); run the daemon with 'axon start --env $ENVFILE'"
fi

# ── Summary ─────────────────────────────────────────────────────────────────
step "Done"
ok "binary:    $BIN"
ok "config:    $CONFIG"
ok "secrets:   $ENVFILE"
ok "profile:   $PROFILE   (data in $DATADIR)"
[ "$DO_SERVICE" -eq 1 ] && ok "dashboard: http://127.0.0.1:$PORT"
cat <<EOF

  Next steps:
    • axon doctor                 # health check (claude/ollama/ports/vault)
    • axon status                 # daemon health + budget + last runs
    • open Claude Code in your vault for interactive use
  Manage the daemon:
    • launchctl stop  com.axon.$PROFILE      # stop now (launchd restarts at login)
    • launchctl unload $PLIST                # disable auto-start
  Uninstall everything:
    • scripts/uninstall-macos.sh             # add --purge to also delete $AXON_HOME
EOF

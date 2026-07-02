#!/usr/bin/env bash
#
# install-macos.sh — macOS setup for AXON FROM SOURCE (repo checkout + Go/Node).
#
# Not building from source? The one-liner release install is simpler:
#   curl -fsSL https://raw.githubusercontent.com/jandro-es/axon/main/install.sh | bash
# and later lifecycle is in the binary itself: axon setup / update / uninstall.
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
#   --embeddings P   embeddings provider: ollama or apple (default: ask on a fresh
#                    interactive install, else keep what the config says)
#   --skip-build     use an existing ./axon build instead of rebuilding
#   -h, --help       show this help
#
# Uninstall with: scripts/uninstall-macos.sh

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
DO_OLLAMA=1
SKIP_BUILD=0
EMBEDDINGS=""

usage() { sed -n '3,20p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit "${1:-0}"; }

while [ $# -gt 0 ]; do
  case "$1" in
    --prefix)    PREFIX="$2"; shift 2 ;;
    --profile)   PROFILE="$2"; shift 2 ;;
    --no-service) DO_SERVICE=0; shift ;;
    --no-ollama)  DO_OLLAMA=0; shift ;;
    --embeddings) EMBEDDINGS="$2"; shift 2 ;;
    --skip-build) SKIP_BUILD=1; shift ;;
    -h|--help)   usage 0 ;;
    *) err "unknown option: $1"; usage 1 ;;
  esac
done
case "$EMBEDDINGS" in ""|ollama|apple) : ;; *) err "--embeddings must be ollama or apple"; usage 1 ;; esac

BIN="$PREFIX/bin/axon"
CONFIG="$AXON_HOME/config.yaml"
ENVFILE="$AXON_HOME/.env"
# axon resolves its own config/env; we pass these so the script is robust to a
# non-default AXON_HOME and so every invocation is explicit.
AX=( "$BIN" --config "$CONFIG" --env "$ENVFILE" )
[ -n "$PROFILE" ] && AX+=( --profile "$PROFILE" )

require_macos
printf '%sAXON macOS installer%s  (prefix=%s, home=%s)\n' "$_C_BOLD" "$_C_RESET" "$PREFIX" "$AXON_HOME"

# If AXON is already installed, this re-run is a convergence; point the user at
# the dedicated updater (which also restarts the daemon and reports the delta).
if [ "$SKIP_BUILD" -eq 0 ] && [ -x "$BIN" ] && [ -f "$CONFIG" ]; then
  info "AXON $(axon_installed_version "$PREFIX") already installed — re-running is safe (idempotent)."
  info "to update an existing install, 'make update' gives a cleaner delta + daemon restart."
fi

# ── 1. Prerequisites ────────────────────────────────────────────────────────
set_ctx "checking prerequisites"
step "Checking prerequisites"
# preflight.sh reports every build + runtime dependency with OS-specific install
# instructions, and exits non-zero only when a REQUIRED dep (Go) is missing.
bash "$SCRIPT_DIR/preflight.sh" --all \
  || die "a required dependency is missing (see above) — install it and re-run 'make setup'"

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

# ── Embeddings provider choice ──────────────────────────────────────────────
# Config stays the source of truth: an explicit choice (flag, or the prompt on
# a fresh interactive install) is persisted by `axon init --embeddings` below;
# otherwise whatever the config already says is converged unchanged.
set_ctx "choosing the embeddings provider"
step "Embeddings provider"
EMBEDDINGS_EXPLICIT=1
if [ -z "$EMBEDDINGS" ]; then
  if [ "$created_config" -eq 1 ] && [ -t 0 ]; then
    if confirm "Use Apple's on-device model for embeddings instead of Ollama? (no Ollama server needed; requires Xcode CLT)"; then
      EMBEDDINGS=apple
    else
      EMBEDDINGS=ollama
    fi
  else
    EMBEDDINGS="$("${AX[@]}" config get embeddings.provider 2>/dev/null || echo ollama)"
    EMBEDDINGS_EXPLICIT=0
    skip "keeping configured provider '$EMBEDDINGS' (choose with --embeddings ollama|apple)"
  fi
fi
if [ "$EMBEDDINGS" = apple ]; then
  DO_OLLAMA=0
  xcode-select -p >/dev/null 2>&1 \
    || warn "Xcode Command Line Tools not found — the helper build in 'axon init' will warn until you run: $(install_hint xcode-clt)"
else
  # Convenience: offer to install Ollama via Homebrew if it's missing.
  if [ "$DO_OLLAMA" -eq 1 ] && ! have ollama; then
    if have brew && confirm "Install Ollama now via Homebrew?"; then
      info "installing Ollama…"
      brew install ollama && ok "ollama installed" \
        || warn "Ollama install failed — install it manually ($(install_hint ollama)); not required until embeddings run"
    else
      warn "Ollama not installed — add it later with: $(install_hint ollama)"
    fi
  fi
fi
ok "embeddings provider: $EMBEDDINGS"

# Resolve the values we need for service + ollama from the live config.
PROFILE="${PROFILE:-$("${AX[@]}" config get active_profile 2>/dev/null || echo personal)}"
MODEL="$("${AX[@]}" config get embeddings.model 2>/dev/null || echo nomic-embed-text)"
PORT="$("${AX[@]}" config get dashboard.port 2>/dev/null || echo 7777)"
DATADIR="$("${AX[@]}" config get data_dir 2>/dev/null || echo "$AXON_HOME/profiles/$PROFILE")"
DATADIR="${DATADIR/#\~/$HOME}"   # expand a leading ~ for display/log paths
PLIST="$HOME/Library/LaunchAgents/com.axon.$PROFILE.plist"

# ── 5. Ollama at login + embedding model (skipped for the apple provider) ───
if [ "$EMBEDDINGS" != apple ] && [ "$DO_OLLAMA" -eq 1 ] && have ollama; then
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
if [ "$EMBEDDINGS_EXPLICIT" -eq 1 ]; then
  "${AX[@]}" init --embeddings "$EMBEDDINGS"   # persists the choice, then streams its ✓/↻/⚠/✗ report
else
  "${AX[@]}" init   # streams its own ✓/↻/⚠/✗ report
fi

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

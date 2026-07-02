#!/usr/bin/env bash
#
# preflight.sh — check the tools AXON needs to build and run, and tell you
# exactly how to install anything that's missing (using your OS's package
# manager). Safe to run any time; changes nothing.
#
# Usage: scripts/preflight.sh [--build|--runtime|--all] [-q|--quiet]
#   --build     only the build toolchain (Go, Node, make, git)
#   --runtime   only the runtime dependencies (claude, ollama, curl)
#   --all       both (default)
#   -q, --quiet only print problems (no ✓ lines)
#
# Exit status: 0 if every REQUIRED dependency is present, 1 otherwise.
# (Only Go is strictly required to build; everything else is recommended and
# reported as a warning so you can proceed and add it later.)

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=scripts/_common.sh
. "$SCRIPT_DIR/_common.sh"

SCOPE="all"
QUIET=0
GO_MIN="1.26"

while [ $# -gt 0 ]; do
  case "$1" in
    --build)   SCOPE="build"; shift ;;
    --runtime) SCOPE="runtime"; shift ;;
    --all)     SCOPE="all"; shift ;;
    -q|--quiet) QUIET=1; shift ;;
    -h|--help) sed -n '3,15p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) err "unknown option: $1"; exit 2 ;;
  esac
done

MISSING_REQUIRED=0
MISSING_OPTIONAL=0

# report_ok NAME DETAIL
report_ok() { [ "$QUIET" -eq 1 ] || ok "$1 ${2:+— $2}"; }

# report_bad LEVEL NAME WHY TOOL — LEVEL is "required" or "optional".
report_bad() {
  local level="$1" name="$2" why="$3" tool="$4"
  if [ "$level" = required ]; then
    err "$name — $why"
    MISSING_REQUIRED=$((MISSING_REQUIRED + 1))
  else
    warn "$name — $why"
    MISSING_OPTIONAL=$((MISSING_OPTIONAL + 1))
  fi
  info "install: $(install_hint "$tool")"
}

check_go() {
  if ! have go; then
    report_bad required "Go toolchain" "not found (need $GO_MIN+)" go
    return
  fi
  local v; v="$(go_version)"
  if [ -n "$v" ] && ! version_ge "$v" "$GO_MIN"; then
    report_bad required "Go $v" "too old — need $GO_MIN+" go
    return
  fi
  report_ok "Go $v"
}

check_node() {
  if have node && have npm; then
    report_ok "Node $(node --version 2>/dev/null)" "npm $(npm --version 2>/dev/null)"
  else
    report_bad optional "Node/npm" "not found — the binary builds without the dashboard SPA (a fallback page is served)" node
  fi
}

check_simple() { # NAME TOOL LEVEL DESC
  local name="$1" tool="$2" level="$3" desc="$4"
  if have "$tool"; then
    report_ok "$name"
  else
    report_bad "$level" "$name" "$desc" "$tool"
  fi
}

echo
printf '%sAXON preflight%s  (%s, %s)\n' "$_C_BOLD" "$_C_RESET" "$(axon_os)" "$(axon_arch)"

if [ "$SCOPE" = build ] || [ "$SCOPE" = all ]; then
  step "Build toolchain"
  check_go
  check_node
  check_simple "make" make optional "recommended for the build shortcuts (you can still 'go build')"
  check_simple "git" git optional "recommended so the build can stamp a version"
fi

if [ "$SCOPE" = runtime ] || [ "$SCOPE" = all ]; then
  step "Runtime dependencies"
  if have claude; then report_ok "claude CLI"
  else report_bad optional "claude CLI" "needed for automations + interactive use" claude; fi
  if have ollama; then report_ok "ollama"
  else report_bad optional "ollama" "needed for local embeddings + hybrid search" ollama; fi
  if [ "$(axon_os)" = macos ]; then
    if have swiftc; then report_ok "swiftc" "enables the 'apple' embeddings provider"
    else report_bad optional "swiftc" "only needed for embeddings.provider: apple" xcode-clt; fi
  fi
  check_simple "curl" curl optional "used by the installer to health-check the daemon" curl
fi

step "Summary"
if [ "$MISSING_REQUIRED" -eq 0 ] && [ "$MISSING_OPTIONAL" -eq 0 ]; then
  ok "all dependencies present — you're ready to build and run AXON"
elif [ "$MISSING_REQUIRED" -eq 0 ]; then
  ok "all REQUIRED dependencies present ($MISSING_OPTIONAL optional missing — see ⚠ above)"
  info "AXON will build and run; add the optional tools above for the full experience"
else
  err "$MISSING_REQUIRED required dependenc$([ "$MISSING_REQUIRED" -eq 1 ] && echo y || echo ies) missing — install the ✗ item(s) above, then re-run"
fi

if [ "$MISSING_REQUIRED" -eq 0 ]; then exit 0; else exit 1; fi

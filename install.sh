#!/usr/bin/env bash
#
# install.sh — one-liner AXON installer (no Go/Node/repo needed).
#
#   curl -fsSL https://raw.githubusercontent.com/jandro-es/axon/main/install.sh | bash
#
# Downloads the latest release binary for this platform, verifies its SHA-256
# against the release's checksums.txt, installs it, and hands over to the
# interactive `axon setup` (config, secrets, vault, index, service).
#
# Options:
#   --user       install to ~/.local/bin instead of /usr/local/bin (no sudo)
#   --no-setup   install the binary only; run `axon setup` yourself later
#
# Building from source instead? Clone the repo and run `make setup`
# (see scripts/install-macos.sh / scripts/install-linux.sh).

set -euo pipefail

REPO="jandro-es/axon"
PREFIX="/usr/local/bin"
RUN_SETUP=1

while [ $# -gt 0 ]; do
  case "$1" in
    --user)     PREFIX="$HOME/.local/bin"; shift ;;
    --no-setup) RUN_SETUP=0; shift ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

case "$(uname -s)" in
  Darwin) OS=darwin ;;
  Linux)  OS=linux ;;
  *) echo "unsupported OS: $(uname -s) (darwin/linux only)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
  arm64|aarch64) ARCH=arm64 ;;
  x86_64|amd64)  ARCH=amd64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

command -v curl >/dev/null || { echo "curl is required" >&2; exit 1; }
command -v shasum >/dev/null || command -v sha256sum >/dev/null || { echo "shasum or sha256sum is required" >&2; exit 1; }

echo "→ resolving the latest AXON release…"
TAG="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
  | grep -m1 '"tag_name"' | cut -d'"' -f4)"
[ -n "$TAG" ] || { echo "could not resolve the latest release (no releases yet?)" >&2; exit 1; }
VERSION="${TAG#v}"
ASSET="axon_${VERSION}_${OS}_${ARCH}"
BASE="https://github.com/$REPO/releases/download/$TAG"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

echo "→ downloading $ASSET ($TAG)…"
curl -fsSL -o "$TMP/$ASSET" "$BASE/$ASSET"
curl -fsSL -o "$TMP/checksums.txt" "$BASE/checksums.txt"

echo "→ verifying checksum…"
( cd "$TMP" && grep " $ASSET\$" checksums.txt | { shasum -a 256 -c - 2>/dev/null || sha256sum -c -; } ) \
  || { echo "✗ checksum verification FAILED — aborting" >&2; exit 1; }

echo "→ installing to $PREFIX/axon…"
chmod 0755 "$TMP/$ASSET"
if [ -w "$PREFIX" ] || { mkdir -p "$PREFIX" 2>/dev/null && [ -w "$PREFIX" ]; }; then
  mv "$TMP/$ASSET" "$PREFIX/axon"
else
  echo "  (needs sudo for $PREFIX)"
  sudo install -d "$PREFIX"
  sudo install -m 0755 "$TMP/$ASSET" "$PREFIX/axon"
fi
echo "✓ installed $("$PREFIX/axon" version --short 2>/dev/null || echo axon) at $PREFIX/axon"
case ":$PATH:" in *":$PREFIX:"*) : ;; *) echo "⚠ $PREFIX is not on your PATH — add it to your shell profile." ;; esac

if [ "$RUN_SETUP" -eq 1 ] && [ -t 0 ]; then
  echo "→ handing over to axon setup…"
  exec "$PREFIX/axon" setup
else
  echo "→ next: run 'axon setup' to provision your vault, config and daemon."
fi

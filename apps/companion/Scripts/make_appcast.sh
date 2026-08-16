#!/usr/bin/env bash
# Generate/extend the Sparkle appcast from a signed release zip.
#
# Sparkle's `generate_appcast` signs each entry with the EdDSA private key in
# the release machine's keychain; the public half is baked into the app's
# Info.plist as SUPublicEDKey. An install only accepts an update whose
# signature matches the key it shipped with, which is why that private key must
# be backed up (see version.env).
#
# Usage: Scripts/make_appcast.sh [dist/Axon-<version>.zip]
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
# shellcheck source=/dev/null
source "$ROOT/version.env"

DIST="$ROOT/${DIST_DIR:-dist}"
ZIP="${1:-$DIST/${APP_NAME}-${MARKETING_VERSION}.zip}"
APPCAST="$DIST/companion-appcast.xml"

log() { printf '\033[1mappcast:\033[0m %s\n' "$*"; }
die() { printf '\033[1;31mappcast: ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

[[ -f "$ZIP" ]] || die "no release zip at $ZIP — run Scripts/sign-and-notarize.sh first"

GENERATE=$(find "$ROOT/.build" -path '*/sparkle/Sparkle/bin/generate_appcast' -type f 2>/dev/null | head -1)
if [[ -z "$GENERATE" ]]; then
  GENERATE=$(command -v generate_appcast || true)
fi
[[ -n "$GENERATE" ]] || die "generate_appcast not found. Run 'swift package resolve' to fetch Sparkle's tools."

# A build number that did not increase is invisible to every existing install,
# so refuse rather than publish an update nobody receives.
BUILD_IN_APP=$(/usr/libexec/PlistBuddy -c 'Print :CFBundleVersion' \
  "$DIST/${APP_NAME}.app/Contents/Info.plist" 2>/dev/null || echo "$BUILD_NUMBER")
if [[ -f "$APPCAST" ]]; then
  # generate_appcast emits <sparkle:version>N</sparkle:version> as an ELEMENT,
  # not an attribute. Matching the attribute form found nothing, so the guard
  # silently passed and republished a build no install would ever see.
  PREVIOUS=$(grep -oE '<sparkle:version>[0-9]+</sparkle:version>' "$APPCAST" \
    | grep -oE '[0-9]+' | sort -n | tail -1 || true)
  if [[ -n "${PREVIOUS:-}" ]] && [[ "$BUILD_IN_APP" -le "$PREVIOUS" ]]; then
    die "BUILD_NUMBER $BUILD_IN_APP does not exceed the newest appcast entry ($PREVIOUS).
  Sparkle compares CFBundleVersion; this update would reach nobody.
  Bump BUILD_NUMBER in version.env and rebuild."
  fi
fi

# generate_appcast scans a directory, so give it one holding only this release
# plus any existing appcast to extend.
WORK=$(mktemp -d /tmp/axon-appcast.XXXXXX)
trap 'rm -rf "$WORK"' EXIT
cp "$ZIP" "$WORK/"
if [[ -f "$APPCAST" ]]; then
  cp "$APPCAST" "$WORK/companion-appcast.xml"
fi

log "signing $(basename "$ZIP")"
"$GENERATE" \
  --download-url-prefix "$SPARKLE_DOWNLOAD_URL_PREFIX" \
  --link "https://github.com/jandro-es/axon" \
  -o "$WORK/companion-appcast.xml" \
  "$WORK"

mkdir -p "$DIST"
cp "$WORK/companion-appcast.xml" "$APPCAST"

log "wrote $APPCAST"
log "upload it plus $(basename "$ZIP") to the companion-v${MARKETING_VERSION} release"

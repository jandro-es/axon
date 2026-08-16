#!/usr/bin/env bash
# Build, Developer ID sign, notarize, staple and verify a release build.
#
# Two credential paths, checked in this order:
#
#   1. A notarytool keychain profile (default: $NOTARY_PROFILE, else the first
#      of axon-notary / spinnaker-notary that works). This is the developer-
#      machine path; the credentials never touch the repo or the environment.
#      Create one once with:
#        xcrun notarytool store-credentials axon-notary \
#          --apple-id <apple-id> --team-id <TEAM_ID> \
#          --password <app-specific-password>
#
#   2. App Store Connect API key env vars, for CI where no keychain exists:
#      APP_STORE_CONNECT_API_KEY_P8 / _KEY_ID / _ISSUER_ID.
#
# Environment:
#   APP_IDENTITY   codesign identity. Default: the first "Developer ID
#                  Application" certificate in the keychain.
#   NOTARY_PROFILE notarytool keychain profile name.
#   ARCHES         space-separated arches. Default: universal (arm64 x86_64).
#   SKIP_NOTARIZE  set to 1 to sign and package without submitting to Apple.
set -euo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
source "$ROOT/version.env"

APP_NAME=${APP_NAME:-Axon}
DIST="$ROOT/${DIST_DIR:-dist}"
APP_BUNDLE="$DIST/${APP_NAME}.app"
ZIP_NAME="$DIST/${APP_NAME}-${MARKETING_VERSION}.zip"
APP_ENTITLEMENTS=${APP_ENTITLEMENTS:-$ROOT/Companion.entitlements}
DITTO_BIN=${DITTO_BIN:-/usr/bin/ditto}

log()  { printf '\033[1msign:\033[0m %s\n' "$*"; }
die()  { printf '\033[1;31msign: ERROR:\033[0m %s\n' "$*" >&2; exit 1; }

# ---------------------------------------------------------------- identity ---

if [[ -z "${APP_IDENTITY:-}" ]]; then
  APP_IDENTITY=$(security find-identity -v -p codesigning 2>/dev/null \
    | awk -F'"' '/Developer ID Application/{print $2; exit}')
fi
[[ -n "$APP_IDENTITY" ]] || die "no 'Developer ID Application' certificate in the keychain.
  Notarization requires one; an Apple Development certificate is rejected.
  Set APP_IDENTITY to override the automatic choice."
log "identity: $APP_IDENTITY"

# --------------------------------------------------------------- build/sign ---

# Universal by default: a Developer ID build is what other people run, and an
# arm64-only download is a silent failure on an Intel Mac.
ARCHES_VALUE=${ARCHES:-"arm64 x86_64"}
log "building ($ARCHES_VALUE)"
APP_IDENTITY="$APP_IDENTITY" ARCHES="$ARCHES_VALUE" "$ROOT/Scripts/package_app.sh" release

# package_app.sh already signed with this identity; re-sign only to guarantee
# the entitlements and hardened runtime are exactly what this script intends,
# independent of how package_app.sh was configured.
codesign --force --timestamp --options runtime --sign "$APP_IDENTITY" \
  --entitlements "$APP_ENTITLEMENTS" "$APP_BUNDLE"
codesign --verify --deep --strict --verbose=1 "$APP_BUNDLE"

if [[ "${SKIP_NOTARIZE:-0}" == "1" ]]; then
  log "SKIP_NOTARIZE=1 — signed but not submitted"
  "$DITTO_BIN" --norsrc -c -k --keepParent "$APP_BUNDLE" "$ZIP_NAME"
  log "wrote $ZIP_NAME (NOT notarized; Gatekeeper will block it on other Macs)"
  exit 0
fi

# ------------------------------------------------------------- credentials ---

NOTARY_ARGS=()
if [[ -n "${APP_STORE_CONNECT_API_KEY_P8:-}" ]]; then
  [[ -n "${APP_STORE_CONNECT_KEY_ID:-}" && -n "${APP_STORE_CONNECT_ISSUER_ID:-}" ]] \
    || die "APP_STORE_CONNECT_API_KEY_P8 set without _KEY_ID and _ISSUER_ID"
  KEY_FILE=$(mktemp /tmp/asc-key.XXXXXX.p8)
  trap 'rm -f "$KEY_FILE" "$SUBMIT_ZIP"' EXIT
  printf '%s' "$APP_STORE_CONNECT_API_KEY_P8" | sed 's/\\n/\n/g' > "$KEY_FILE"
  NOTARY_ARGS=(--key "$KEY_FILE"
               --key-id "$APP_STORE_CONNECT_KEY_ID"
               --issuer "$APP_STORE_CONNECT_ISSUER_ID")
  log "credentials: App Store Connect API key"
else
  # Try the configured profile, then the known ones. Same Apple ID and team
  # back all of them, so an existing profile is reused rather than making the
  # owner store a second identical credential.
  for candidate in "${NOTARY_PROFILE:-}" axon-notary spinnaker-notary; do
    [[ -n "$candidate" ]] || continue
    if xcrun notarytool history --keychain-profile "$candidate" >/dev/null 2>&1; then
      NOTARY_ARGS=(--keychain-profile "$candidate")
      log "credentials: keychain profile '$candidate'"
      break
    fi
  done
  [[ ${#NOTARY_ARGS[@]} -gt 0 ]] || die "no usable notary credentials.
  Store them once with:
    xcrun notarytool store-credentials axon-notary \\
      --apple-id <apple-id> --team-id <TEAM_ID> --password <app-specific-password>
  Or set NOTARY_PROFILE to an existing profile, or the APP_STORE_CONNECT_* vars."
fi

# --------------------------------------------------------------- notarize ---

SUBMIT_ZIP=$(mktemp /tmp/"${APP_NAME}"-notarize.XXXXXX.zip)
rm -f "$SUBMIT_ZIP"
"$DITTO_BIN" --norsrc -c -k --keepParent "$APP_BUNDLE" "$SUBMIT_ZIP"

log "submitting to Apple (typically 1-5 minutes)"
xcrun notarytool submit "$SUBMIT_ZIP" "${NOTARY_ARGS[@]}" --wait

# Stapling lets the app launch on a machine that is offline or behind a
# firewall that blocks Apple's ticket lookup.
xcrun stapler staple "$APP_BUNDLE"

# Re-zip AFTER stapling: the submitted archive predates the ticket, so shipping
# it would distribute an unstapled app that had in fact been notarized.
xattr -cr "$APP_BUNDLE"
find "$APP_BUNDLE" -name '._*' -delete
rm -f "$ZIP_NAME"
"$DITTO_BIN" --norsrc -c -k --keepParent "$APP_BUNDLE" "$ZIP_NAME"

# ----------------------------------------------------------------- verify ---

log "verifying"
spctl -a -t exec -vv "$APP_BUNDLE"
xcrun stapler validate "$APP_BUNDLE"

log "done: $ZIP_NAME"

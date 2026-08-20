#!/usr/bin/env bash
set -euo pipefail

CONF=${1:-release}
ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"

APP_NAME=${APP_NAME:-Axon}
# The SwiftPM executable product name. It differs from the bundle/display
# name on purpose: the target is "Companion", the app the user sees is "Axon".
EXECUTABLE_NAME=${EXECUTABLE_NAME:-$APP_NAME}
BUNDLE_ID=${BUNDLE_ID:-com.axon.companion}
MACOS_MIN_VERSION=${MACOS_MIN_VERSION:-26.0}
MENU_BAR_APP=${MENU_BAR_APP:-0}
SIGNING_MODE=${SIGNING_MODE:-}
APP_IDENTITY=${APP_IDENTITY:-}

if [[ -f "$ROOT/version.env" ]]; then
  source "$ROOT/version.env"
else
  MARKETING_VERSION=${MARKETING_VERSION:-0.1.0}
  BUILD_NUMBER=${BUILD_NUMBER:-1}
fi

# Word-splitting ARCHES is the intent: it is a space-separated arch list.
# shellcheck disable=SC2206
ARCH_LIST=( ${ARCHES:-} )
if [[ ${#ARCH_LIST[@]} -eq 0 ]]; then
  HOST_ARCH=$(uname -m)
  ARCH_LIST=("$HOST_ARCH")
fi

# Each arch builds into its OWN scratch path. Swift 6.2+ with the Swift Build
# system writes every arch to the same .build/<conf>/<product>, so building two
# arches in sequence overwrites the first and lipo silently receives the same
# slice twice -- a "universal" binary that is nothing of the sort.
scratch_for() {
  if [[ ${#ARCH_LIST[@]} -gt 1 ]]; then echo "$ROOT/.build/arch-$1"; else echo "$ROOT/.build"; fi
}

for ARCH in "${ARCH_LIST[@]}"; do
  # The Swift Build backend is load-bearing: it is what emits the per-module
  # .swiftconstvalues that appintentsmetadataprocessor consumes (CFR-92…95).
  # The legacy SwiftPM build system emits none and Siri would silently see no
  # intents.
  swift build -c "$CONF" --arch "$ARCH" --build-system swiftbuild --scratch-path "$(scratch_for "$ARCH")"
done

DIST="$ROOT/${DIST_DIR:-dist}"
APP="$DIST/${APP_NAME}.app"
rm -rf "$APP"
mkdir -p "$DIST"
mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$APP/Contents/Frameworks"

# Convert Icon.icon to Icon.icns if present (requires iconutil).
# Icon.icns is generated from the dashboard palette by Scripts/build_icon.sh.
# Built on demand so a fresh clone packages a real icon without a manual step.
ICON_TARGET="$ROOT/Icon.icns"
if [[ ! -f "$ICON_TARGET" ]] && [[ -x "$ROOT/Scripts/build_icon.sh" ]]; then
  "$ROOT/Scripts/build_icon.sh" >/dev/null || echo "icon build skipped" >&2
fi

LSUI_VALUE="false"
if [[ "$MENU_BAR_APP" == "1" ]]; then
  LSUI_VALUE="true"
fi

# Sparkle compares CFBundleVersion, so a release whose BUILD_NUMBER did not
# increase is invisible to every existing install. Fail loudly rather than
# shipping an update nobody receives.
if [[ -n "${REQUIRE_BUILD_NUMBER_ABOVE:-}" ]]; then
  if [[ "$BUILD_NUMBER" -le "$REQUIRE_BUILD_NUMBER_ABOVE" ]]; then
    echo "ERROR: BUILD_NUMBER ($BUILD_NUMBER) must exceed the last release ($REQUIRE_BUILD_NUMBER_ABOVE)." >&2
    echo "       Sparkle compares CFBundleVersion; existing installs would never see this update." >&2
    exit 1
  fi
fi

BUILD_TIMESTAMP=$(date -u +"%Y-%m-%dT%H:%M:%SZ")
GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")

cat > "$APP/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>CFBundleName</key><string>${APP_NAME}</string>
    <key>CFBundleDisplayName</key><string>${APP_NAME}</string>
    <key>CFBundleIdentifier</key><string>${BUNDLE_ID}</string>
    <key>CFBundleExecutable</key><string>${APP_NAME}</string>
    <key>CFBundlePackageType</key><string>APPL</string>
    <key>CFBundleShortVersionString</key><string>${MARKETING_VERSION}</string>
    <key>CFBundleVersion</key><string>${BUILD_NUMBER}</string>
    <key>LSMinimumSystemVersion</key><string>${MACOS_MIN_VERSION}</string>
    <key>LSUIElement</key><${LSUI_VALUE}/>
    <key>CFBundleIconFile</key><string>Icon</string>
    <key>BuildTimestamp</key><string>${BUILD_TIMESTAMP}</string>
    <key>GitCommit</key><string>${GIT_COMMIT}</string>
    <!-- Sparkle 2. The feed is the only non-loopback host this app contacts. -->
    <key>SUFeedURL</key><string>${SPARKLE_FEED_URL}</string>
    <key>SUPublicEDKey</key><string>${SPARKLE_PUBLIC_KEY}</string>
    <!-- Opt in by default, but never silently: Sparkle asks on first run. -->
    <key>SUEnableAutomaticChecks</key><true/>
    <key>SUScheduledCheckInterval</key><integer>86400</integer>
</dict>
</plist>
PLIST

# Resolve a built product, probing every layout SwiftPM has used. Swift 6.2+
# with the Swift Build system emits .build/out/Products/<Conf>/<name> (with
# .build/<conf> symlinked to it) and does NOT create arch-specific directories;
# the legacy build system emits .build/<arch>-apple-macosx/<conf>/<name>.
# Checking the arch path first keeps multi-arch (lipo) builds working wherever
# the legacy layout is still produced.
build_product_path() {
  local name="$1"
  local arch="$2"
  local scratch conf_dir
  scratch="$(scratch_for "$arch")"
  # .build/{debug,release} is lowercase; .build/out/Products/{Debug,Release} is not.
  conf_dir="$(tr '[:lower:]' '[:upper:]' <<<"${CONF:0:1}")${CONF:1}"
  local candidates=(
    "${scratch}/${arch}-apple-macosx/$CONF/$name"
    "${scratch}/$CONF/$name"
    "${scratch}/out/Products/${conf_dir}/$name"
  )
  local c
  for c in "${candidates[@]}"; do
    if [[ -f "$c" ]]; then
      echo "$c"
      return 0
    fi
  done
  # Nothing found: echo the canonical path so the caller's error names it.
  echo "${candidates[0]}"
}

verify_binary_arches() {
  local binary="$1"; shift
  local expected=("$@")
  local actual
  actual=$(lipo -archs "$binary")
  local actual_count expected_count
  actual_count=$(wc -w <<<"$actual" | tr -d ' ')
  expected_count=${#expected[@]}
  if [[ "$actual_count" -ne "$expected_count" ]]; then
    echo "ERROR: $binary arch mismatch (expected: ${expected[*]}, actual: ${actual})" >&2
    exit 1
  fi
  for arch in "${expected[@]}"; do
    if [[ "$actual" != *"$arch"* ]]; then
      echo "ERROR: $binary missing arch $arch (have: ${actual})" >&2
      exit 1
    fi
  done
}

install_binary() {
  local name="$1"
  local dest="$2"
  local binaries=()
  for arch in "${ARCH_LIST[@]}"; do
    local src
    src=$(build_product_path "$name" "$arch")
    if [[ ! -f "$src" ]]; then
      echo "ERROR: Missing ${name} build for ${arch} at ${src}" >&2
      exit 1
    fi
    binaries+=("$src")
  done
  if [[ ${#ARCH_LIST[@]} -gt 1 ]]; then
    # Guard against two "different" arches resolving to one file.
    local unique
    unique=$(printf '%s\n' "${binaries[@]}" | sort -u | wc -l | tr -d ' ')
    if [[ "$unique" -ne ${#binaries[@]} ]]; then
      echo "ERROR: arch builds collided on the same path; universal build would be fake" >&2
      printf '  %s\n' "${binaries[@]}" >&2
      exit 1
    fi
    lipo -create "${binaries[@]}" -output "$dest"
  else
    cp "${binaries[0]}" "$dest"
  fi
  chmod +x "$dest"
  verify_binary_arches "$dest" "${ARCH_LIST[@]}"
}

install_binary "$EXECUTABLE_NAME" "$APP/Contents/MacOS/$APP_NAME"

# Bundle app resources (if any).
APP_RESOURCES_DIR="$ROOT/Sources/$EXECUTABLE_NAME/Resources"
if [[ -d "$APP_RESOURCES_DIR" ]]; then
  cp -R "$APP_RESOURCES_DIR/." "$APP/Contents/Resources/"
fi

# SwiftPM resource bundles are emitted next to the built binary.
PREFERRED_BUILD_DIR="$(dirname "$(build_product_path "$EXECUTABLE_NAME" "${ARCH_LIST[0]}")")"
shopt -s nullglob
SWIFTPM_BUNDLES=("${PREFERRED_BUILD_DIR}/"*.bundle)
shopt -u nullglob
if [[ ${#SWIFTPM_BUNDLES[@]} -gt 0 ]]; then
  for bundle in "${SWIFTPM_BUNDLES[@]}"; do
    cp -R "$bundle" "$APP/Contents/Resources/"
  done
fi

# Embed frameworks if any exist in the build folder.
FRAMEWORK_DIRS=("$(scratch_for "${ARCH_LIST[0]}")/$CONF" "$(scratch_for "${ARCH_LIST[0]}")/${ARCH_LIST[0]}-apple-macosx/$CONF")
for dir in "${FRAMEWORK_DIRS[@]}"; do
  if compgen -G "${dir}/*.framework" >/dev/null; then
    cp -R "${dir}/"*.framework "$APP/Contents/Frameworks/"
    chmod -R a+rX "$APP/Contents/Frameworks"
    install_name_tool -add_rpath "@executable_path/../Frameworks" "$APP/Contents/MacOS/$APP_NAME"
    break
  fi
done

if [[ -f "$ICON_TARGET" ]]; then
  cp "$ICON_TARGET" "$APP/Contents/Resources/Icon.icns"
fi

# App Intents metadata (CFR-92…95): Siri/Shortcuts discover intents through
# Metadata.appintents, which Xcode's build emits and a bare SwiftPM bundle
# does not. Run the extractor explicitly; a missing bundle means Siri sees
# nothing, so failure here fails packaging loudly.
extract_appintents_metadata() {
  local scratch conf_dir triple sdk_root toolchain_dir xcode_build listdir
  scratch="$(scratch_for "${ARCH_LIST[0]}")"
  conf_dir="$(tr '[:lower:]' '[:upper:]' <<<"${CONF:0:1}")${CONF:1}"
  triple="${ARCH_LIST[0]}-apple-macos26.0"
  sdk_root="$(xcrun --show-sdk-path --sdk macosx)"
  toolchain_dir="$(dirname "$(dirname "$(dirname "$(xcrun -f swiftc)")")")"
  xcode_build="$(xcodebuild -version 2>/dev/null | awk '/Build version/{print $3}')"
  listdir="$scratch/appintents-lists"
  mkdir -p "$listdir"

  find "$ROOT/Sources/$EXECUTABLE_NAME" -name '*.swift' > "$listdir/sources.txt"
  find "$scratch" -path "*${conf_dir}*" -name '*.swiftconstvalues' > "$listdir/constvals.txt"
  if [[ ! -s "$listdir/constvals.txt" ]]; then
    echo "ERROR: no .swiftconstvalues under $scratch — the build did not run on the Swift Build backend; App Intents metadata cannot be extracted" >&2
    exit 1
  fi

  xcrun appintentsmetadataprocessor \
    --output "$APP/Contents/Resources" \
    --toolchain-dir "$toolchain_dir" \
    --module-name "$EXECUTABLE_NAME" \
    --sdk-root "$sdk_root" \
    --xcode-version "${xcode_build:-unknown}" \
    --platform-family macOS \
    --deployment-target 26.0 \
    --target-triple "$triple" \
    --source-file-list "$listdir/sources.txt" \
    --swift-const-vals-list "$listdir/constvals.txt" \
    --force --quiet-warnings

  if [[ ! -d "$APP/Contents/Resources/Metadata.appintents" ]]; then
    echo "ERROR: appintentsmetadataprocessor produced no Metadata.appintents — Siri would silently see no intents" >&2
    exit 1
  fi
}
extract_appintents_metadata

# Ensure contents are writable before stripping attributes and signing.
chmod -R u+w "$APP"

# Strip extended attributes to prevent AppleDouble files that break code sealing.
xattr -cr "$APP"
find "$APP" -name '._*' -delete

# The committed entitlements file is the source of truth; it documents WHY the
# app is not sandboxed, which an auto-generated empty plist cannot.
APP_ENTITLEMENTS=${APP_ENTITLEMENTS:-$ROOT/Companion.entitlements}
if [[ ! -f "$APP_ENTITLEMENTS" ]]; then
  echo "WARNING: $APP_ENTITLEMENTS missing; signing with empty entitlements" >&2
  APP_ENTITLEMENTS="$ROOT/.build/empty.entitlements"
  mkdir -p "$(dirname "$APP_ENTITLEMENTS")"
  cat > "$APP_ENTITLEMENTS" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <!-- Add entitlements here if needed. -->
</dict>
</plist>
PLIST
fi

if [[ "$SIGNING_MODE" == "adhoc" || -z "$APP_IDENTITY" ]]; then
  CODESIGN_ARGS=(--force --sign "-")
else
  CODESIGN_ARGS=(--force --timestamp --options runtime --sign "$APP_IDENTITY")
fi

# Sign embedded frameworks and their nested binaries before the app bundle.
sign_frameworks() {
  local fw
  for fw in "$APP/Contents/Frameworks/"*.framework; do
    if [[ ! -d "$fw" ]]; then
      continue
    fi
    while IFS= read -r -d '' bin; do
      codesign "${CODESIGN_ARGS[@]}" "$bin"
    done < <(find "$fw" -type f -perm -111 -print0)
    codesign "${CODESIGN_ARGS[@]}" "$fw"
  done
}
sign_frameworks

codesign "${CODESIGN_ARGS[@]}" \
  --entitlements "$APP_ENTITLEMENTS" \
  "$APP"

codesign --verify --deep --strict --verbose=1 "$APP"

echo "Created $APP"

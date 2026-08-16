#!/usr/bin/env bash
# Build Icon.icns from the generated master PNG.
#
# The packaging skill's template drives Icon Composer's `ictool`, which needs a
# hand-authored .icon document. Axon has no design-tool source: the icon is
# generated from the dashboard's own palette by Scripts/make_icon.py, so this
# takes the classic iconset route instead. `sips` and `iconutil` ship with
# macOS, so no Xcode component is required.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

MASTER="build/icon_1024.png"
ICONSET="build/Icon.iconset"
OUT="Icon.icns"

command -v iconutil >/dev/null || { echo "iconutil not found" >&2; exit 1; }

mkdir -p build
python3 Scripts/make_icon.py "$MASTER" 1024

rm -rf "$ICONSET"
mkdir -p "$ICONSET"

# Every size macOS asks for, @1x and @2x. Rendering each from the 1024 master
# rather than doubling the previous one keeps the small variants crisp.
for size in 16 32 128 256 512; do
  sips -z "$size" "$size" "$MASTER" --out "$ICONSET/icon_${size}x${size}.png" >/dev/null
  double=$((size * 2))
  sips -z "$double" "$double" "$MASTER" --out "$ICONSET/icon_${size}x${size}@2x.png" >/dev/null
done

iconutil --convert icns --output "$OUT" "$ICONSET"
echo "wrote $OUT"

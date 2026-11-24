#!/bin/sh
# Assembles the project page (site/) with the README screenshots, the app's fonts and icon into one folder.
#   scripts/site/build.sh [out-dir]      (default: bin/site)
# Used by the GitHub Pages workflow (scripts/publish/pages.yml) and for a local preview:
#   scripts/site/build.sh && python3 -m http.server -d bin/site 8000
set -eu
cd "$(dirname "$0")/../.."
OUT=${1:-bin/site}
rm -rf "$OUT"
mkdir -p "$OUT/img" "$OUT/fonts"
cp site/index.html site/site.css "$OUT/"
cp docs/screenshots/*.png "$OUT/img/"
cp design/icon/cuisle-icon.svg "$OUT/img/icon.svg"
cp design/icon/png/cuisle-32.png "$OUT/img/icon-32.png"
cp design/icon/png/cuisle-256.png "$OUT/img/icon-256.png"
cp frontend/src/assets/fonts/*.woff2 frontend/src/assets/fonts/OFL-*.txt "$OUT/fonts/"
touch "$OUT/.nojekyll"
echo "site: $OUT"

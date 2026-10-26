#!/usr/bin/env bash
# Assemble the two loadable extension directories under extension/dist/.
#   dist/chromium   Chrome, Chromium, Brave, Vivaldi, Edge ("Load unpacked")
#   dist/firefox    Firefox family (about:debugging, or sign it, see README)
set -euo pipefail
cd "$(dirname "$0")"
rm -rf dist
for b in chromium firefox; do
  mkdir -p "dist/$b"
  cp background.js icon.png "dist/$b/"
  cp "manifest.$b.json" "dist/$b/manifest.json"
done
echo "built: $(pwd)/dist/chromium  $(pwd)/dist/firefox"

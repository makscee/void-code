#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
npm ci
npm run build
mkdir -p runtime/cache/node
node_url=$(node -e "const p=require('./scripts/resource-pins.json'); process.stdout.write(p.node.source)")
node_archive="runtime/cache/node/${node_url##*/}"
curl --fail --location --proto '=https' --tlsv1.2 "$node_url" --output "$node_archive"
want=$(node -e "const p=require('./scripts/resource-pins.json'); process.stdout.write(p.node.sourceArchiveSha256)")
echo "$want  $node_archive" | shasum -a 256 -c -
npm ci --prefix runtime/pi --ignore-scripts --no-audit --no-fund
npm run assemble
npm run check:pinned-pi-smoke

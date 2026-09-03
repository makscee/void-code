#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
npm ci
npm run build
npm run setup
npm ci --prefix runtime/pi --ignore-scripts --no-audit --no-fund
npm run assemble
npm run check:pinned-pi-smoke

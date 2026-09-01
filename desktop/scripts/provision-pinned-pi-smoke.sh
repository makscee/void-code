#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
npm ci
npm run build
npm run setup
npm ci --prefix runtime/pi --ignore-scripts --no-audit --no-fund
npm run assemble
npm run check:pinned-pi-smoke
# This check lives on this path because this is the path a Pi version bump goes through, and a bump
# is the only way the bundle's undeclared contract with Pi can break.
npm run check:bundled-pi-smoke

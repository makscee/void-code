#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
npm ci
npm run build
npm run setup
npm ci --prefix runtime/pi --ignore-scripts --no-audit --no-fund
# Both bundle checks live on this path because this is the path a Pi version bump goes through, and
# a bump is the only way the bundle's undeclared contract with Pi can break. The contract check runs
# first: it is the earliest moment Pi's source exists and the last one where the failure still fits
# in a sentence.
node scripts/check-pi-bun-contract.mjs
npm run assemble
npm run check:pinned-pi-smoke
# The target is stated, not inferred: the smoke refuses to guess, because guessing is how a macOS
# runner came to report success for a Windows bundle. Here the answer is genuinely this machine --
# the step runs what it bundles -- so the host is spelled out at the call site rather than assumed
# inside the check. The arch is asked of node so an Intel Mac provisions its own bundle.
npm run check:bundled-pi-smoke -- --target "darwin-$(node -p process.arch)"

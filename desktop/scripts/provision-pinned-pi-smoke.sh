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
# What this run proves, and what it does not, because the answer is not obvious and a reader a month
# from now will otherwise read it as an oversight: macOS ships Pi UNBUNDLED -- the macOS assembly
# stages Pi's installed tree, and only the Windows installer carries a bundle -- so this proves
# nothing about the macOS product. It is here for what it does prove, on every push rather than only
# when a Windows installer is built: that the pinned Pi still serves extensions from inside a bundle.
# That contract is what the Windows product rests on, it can only break when this pin moves, and this
# script is the one path a bump goes through. It is not a substitute for the win32 run --
# desktop-windows-app.yml runs the same check against the bundle that actually ships.
#
# The target is stated rather than inferred: guessing is how a macOS runner came to report success
# for a Windows bundle. Here the answer is genuinely this machine, so it is spelled out at the call
# site; the arch is asked of node so an Intel Mac provisions its own bundle.
npm run check:bundled-pi-smoke -- --target "darwin-$(node -p process.arch)"

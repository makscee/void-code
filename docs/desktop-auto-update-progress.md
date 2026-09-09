# Desktop updater — R1/R2/minimal-R3/R4/R5 implementation

09.09.2026; user approved the contract: «Давай да делаем».
Branch: `work/desktop-auto-update`. Implementation checkpoint: `9d517a7`.

## Delivered in this slice

- `desktop/src/main/desktop-update-trust.ts`: bounded strict UTF-8/duplicate-aware JSON,
  canonical base64, Ed25519 verification before payload interpretation, strict release
  selection, exact supported architecture and forward-compatible unknown targets.
- `desktop/src/main/desktop-update-controller.ts`: validated replay-floor policy,
  persist-before-available, explicit download/consent stages, direct failed-download
  retry, reentrant single flight, cancellation and stale completion containment,
  hard 10-second metadata deadline, immutable-by-copy authorized plans carrying the
  original signed envelope through every downstream main-process callback.
- No imports/wiring from Electron startup, renderer, CLI updater or production release
  workflows. These modules do not make the installed application update itself yet.

## Red-first history

Tests: independent author A (Terra). Implementation: independent author B (Sol).
Coordinator ran all assertions/verification; did not author test or production code.

| test checkpoint | measured RED | implementation |
|---|---|---|
| `32056d4` | 103 assertions fail on missing implementation; no collection errors; strict test types pass | `d918377` |
| `3b95a35` | keyId 65, filename 129 and reentrant check produce 3 failing assertions | `f59787b` |
| `3bb858d` | default and oversized timeout override fail the actual 10-second policy | `55cd9f1` |
| `8669373` | direct retry, signed-envelope forwarding, exact-case filenames: 4 failures | `9d517a7` |

Test-only cleanup commits keep ESLint separate from implementation. `850edc7` is an
explicit mutation-derived coverage pin: normal quit with a ready stage was already
correct, but its deliberately broken version survived the first suite.

## Measured verification

From `desktop/`:

```sh
npm test
npm run build
npm run lint
node node_modules/typescript/bin/tsc --noEmit --target ES2022 --module esnext --moduleResolution bundler --strict --skipLibCheck --allowImportingTsExtensions --types node tests/desktop-update*.ts
```

- Full desktop: **1182 passed, 12 skipped**; all **114 updater tests** pass.
- Build, lint and strict test TypeScript pass.
- Latest focused coverage: **92.29% lines**, **84.48% branches**, **87.03% statements**.
- **25 selected semantic mutants killed** in a separate frozen checkout. This is NOT
  an exhaustive mutation score over every possible source mutation, nor per-test
  contribution attribution. Real key/signature verification is never mocked.
- Go suite was run both untagged and through the repository's fixture-tagged gate;
  Ruby release/desktop CI contract checks passed in preflight.

The Python-only feature-tests AST scanner cannot parse TypeScript; its result is
not reported as a semantic pass. A TypeScript AST check found no literal/self-equality
assertions or catch clauses; strict test typing and semantic mutation review were
also used. Test helpers/types are test-only; production never imports them.

Local `.rails/preflight.sh` runs its self-tests, claims, Go, Ruby and static checks.
It reports reusable-workflow/platform jobs as **НЕ СМОГ** rather than running them.
This is a **partial preflight**, not full cross-platform CI or packaging proof.
Independent assertion-aware RED audit also exercised the original test commits;
its custom runner is deliberately NOT exported into preflight's own self-tests.

## Review findings and boundaries

The first code panel and independent Astra Pass-2 confirmed deadline, direct retry,
and missing signed-envelope preservation defects. They were repaired through the RED
checkpoints above. Distinct-case future filenames were repaired too. Follow-up review
must verify the final frozen diff; passing unit tests is not a release verdict.

The proposed atomic-floor rewrite was not established as a current core defect:
`saveFloor` can reject stale/conflicting writes. **The future durable adapter must
atomically advance the maximum and reject same-version equivocation**, including
restart/concurrent-owner cases; it must not be a last-writer-wins JSON write. No durable
filesystem adapter is implemented or claimed by this slice.

## Still NOT delivered

- S0 native proof on disposable unsigned macOS and Windows N→N+1 installations.
- R6 bounded real HTTP download/redirect handling; R7 private staging and safe extraction.
- Full durable R3 adapter; R8 native helper, process-exit proof, transactional recovery.
- R9 actual Electron IPC, UI and startup schedule; R10 signed publication/channel jobs.
- R11 packaged upgrade/recovery/data-retention witnesses; R12 packaged offline checks.
- Production signing-key enrollment, technical channel release creation, release/push
  of tags, deployment and updating any user's installed app.

See `docs/desktop-auto-update-spec.md` for the complete accepted contract. The next
step is native S0 RED/spike plus the remaining adapters, not publishing this core as
an already working auto-updater. Existing v0.2.53 users still need one manual install
of the first updater-capable release.

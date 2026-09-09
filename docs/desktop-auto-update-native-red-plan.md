# Native S0: independent RED plan

## Scope and ownership

This is an S0 experiment, not an updater design approval. It qualifies a candidate only when real, disposable packaged Electron `N → N+1` passes the cases below on native macOS and Windows. A helper exit code, a spawned PID, or an installer exit code is never a success witness.

* **A owns** `desktop/tests/native-update-probe.test.ts`, optional
  `desktop/tests/native-update-workflow.test.ts`, all immutable fixtures under
  `desktop/tests/fixtures/native-update/`, their Electron bootstrap and package
  harness, and the independent assertions. A first commits fixture tooling;
  A then commits tests-only RED. The RED suite has a deliberate, explicit
  `missing helper entrypoint` failure until B provides it—not an incidental Go
  package/import/build failure.
* **B owns** only `desktop/experiments/update-native/`, including the Go
  helper and its update/recovery behaviour. Tests build that directory with
  `go build -o <private-root>/bin/native-update-probe
  ./desktop/experiments/update-native`; they then invoke that exact binary.
  The bootstrap/fixtures are not the SUT and must not replace B's update or
  recovery logic.
* Each run gets a random disposable fixture identity and bounded watchdog
  deadlines. Those are test data/operational bounds, not product decisions or
  approval gates.

Fixtures use no production app ID, executable/product name, keys, auth,
channel, publication or network. Their capsule root has spaces and Unicode,
e.g. `<tmp>/native update テスト/<run-id>/`; all fixture HOME/userData,
staging, journals, lock, backups, receipts and installer output remain below
it. A capsule marker plus expected fixture identity fences every target. On macOS the
exact target is `<capsule>/installed.app`; on Windows it is `<capsule>/installed`.
The suite must reject an arbitrary target or a non-fixture/production identity.

## Fixture contract

A packages **real minimal Electron applications**, pinned to the repository's
`electron` `41.10.3` and `electron-builder` `26.15.7`, with
`electron-builder --publish never`:

* immutable `N` and `N+1` source directories/versioned package inputs, distinct
  version and `resources/full-resource-marker.txt` contents;
* per run, a generated, unique fixture `appId`/bundle ID, product name,
  executable name and Windows GUID/registry namespace; these values are baked
  into the package inputs, never borrowed from Void Code;
* macOS arm64: unsigned `dir`/zip fixture, `identity: null`, `LSUIElement: true`,
  no BrowserWindow/Dock UI;
* Windows x64: per-user NSIS (`oneClick: false`, no elevation, no elevate
  helper, changeable install directory) and a unique `/D=` target. The test
  passes the exact target as the NSIS-compatible final argument; it does not
  infer a default location.

On boot the real packaged app calls `app.getVersion()`, reads its full resource
marker, and uses `process.arch` and `process.execPath`. It atomically writes a
receipt outside the install tree and stays alive until its fixture-controlled
exit request. Its bootstrap never updates, repairs, restores, or otherwise
acts as an updater.

Fixture modes are immutable package variants. In addition to normal `N` and
`N+1`, they provide a wrong-transaction/wrong-receipt variant and, on Windows,
a custom NSIS `customInstall` partial-failure variant. The latter performs
ordinary electron-builder extraction/registry work, creates a durable
fixture-owned witness **outside** the install root, damages a required
installed resource, and exits nonzero. It is deliberately one reproducible
post-extraction failure, not a claim to simulate every extractor error.

Before every mutation test A manually starts and validates installed `N`:
its receipt must match `N`, identity, canonical executable and resources paths,
arch and marker. That initial manually booted-`N` receipt is distinct from the
`N+1` transaction receipt. The request names this predecessor receipt and its
recorded PID, so a helper cannot guess which `N` is live; S0 permits it to
refuse any live recorded PID. After recovery A performs the same manual
bootstrap validation; B owns how recovery is done.

### Fixture fence and durable state refinement

The capsule marker is the immutable, atomically-created
`<capsule>/.native-update-capsule.json` object with exact schema
`{v:1,markerFile:'.native-update-capsule.json',capsule,targetRel,appId,productName,executableName,registryGuid}`, where `targetRel` is exactly
`installed.app` on macOS and `installed` on Windows.
`capsule` and all request paths must canonicalize below that capsule. The
per-run `appId` has prefix `org.voidcode.fixture.`, while product/executable and
NSIS `guid` are unique too; the fixture records the registry address derived
from this pinned builder GUID, never guesses a registry key. Corrupt or missing
marker/journal/receipt is refusal and must not authorize destructive guesses,
even for a path under userdata.

B's journal is B-owned, but must be persisted before the `before-mutation`
barrier and identify only the fenced target, retained predecessor and requested
transaction. Recovery is idempotent: repeated fresh `recover` converges to one
verified predecessor or a receipt-verified successor, never launches another
app or deletes an unproven source. The fixture harness may set up N, launch and
stop only its recorded children, and inspect its own files, symlinks, modes,
registry and data sentinels; it never updates, recovers, or manufactures a
success receipt.

## Minimal black-box helper protocol

The executable accepts only line-delimited JSON on stdin and writes only
line-delimited JSON events to stdout. It accepts no shell command, arbitrary
path, implicit current directory, network URL, or production identifier.
Unknown fields, malformed JSON, a non-absolute/canonical-capsule path, or an
identity/capsule mismatch fail before mutation. Stderr is diagnostic only.

### Request

Exactly one request is supplied per invocation:

```json
{
  "v": 1,
  "op": "install",
  "transactionId": "run-…",
  "capsule": "/absolute/private/capsule",
  "identity": "org.voidcode.fixture.<run>",
  "from": {"version": "1.0.0", "target": "/…/installed/N.app-or-dir", "package": "/…/N-artifact", "predecessorReceipt": "/…/receipts/preinstall-N.json", "predecessorPid": 123},
  "to": {"version": "1.0.1", "package": "/…/N+1-artifact", "marker": "marker-N+1"},
  "receiptDir": "/…/receipts",
  "barrierDir": "/…/barriers",
  "deadlineMs": 45000
}
```

`op: "recover"` has the same `v`, `transactionId`, `capsule`, `identity`,
`from` (including predecessor receipt/PID reference), `receiptDir`,
`barrierDir`, and `deadlineMs`; it has no `to`. The
caller uses a fresh process for recovery. The exact schema is intentionally
small: it says what is being upgraded and where the test-owned witnesses live,
not which renames, journal format, NSIS switches, or rollback method B uses.
A live preinstalled `N` is a permitted refusal: `install` may fail before its
first mutation rather than claim a future shutdown handshake.

### Events and durable barriers

Before waiting at a fault point, the helper atomically creates
`<barrierDir>/<transactionId>.<name>.json` and emits the same event:

```json
{"v":1,"event":"barrier","transactionId":"run-…","name":"before-mutation"}
```

The test acknowledges a non-fault barrier by atomically creating the adjacent
`…<name>.continue` file. For a fault case it kills the **recorded helper PID**
while the barrier exists and verifies that PID exits; it never kills by name.
The required names are:

* `before-mutation` (all platforms),
* macOS: `after-old-to-backup`, `after-new-to-target-before-commit`.

The helper must not expose a `success` event. It may emit `started`, `refused`,
`failed`, and `exited` for diagnostics only. Tests independently inspect the
actual process liveness, canonical filesystem inventory, installed files,
Windows fixture registry witness, and packaged-app receipt.

### Receipt

The fixture writes `<receiptDir>/<transactionId>.json` by temp-file plus rename:

```json
{
  "v": 1,
  "transactionId": "run-…",
  "identity": "org.voidcode.fixture.<run>",
  "version": "1.0.1",
  "arch": "arm64-or-x64",
  "execPath": "/canonical/installed/executable",
  "resourcesPath": "/canonical/installed/resources",
  "marker": "marker-N+1",
  "bootstrap": "ok",
  "pid": 123
}
```

A valid receipt is accepted only after the test independently canonicalizes and
matches every listed expected value, verifies that the PID is the newly
launched owned packaged app, and confirms the old PID is dead. A stale,
malformed, missing, wrong transaction, wrong identity/version/arch/path/marker
receipt is not success.

## Executable RED matrix

Native cases are enabled only on their native OS with
`VOID_NATIVE_UPDATE_PROBE=1`; otherwise each native case is an explicit skip.
The ordinary `npm test` may run only a small source/contract-presence test.
It must not turn skipped native qualification into a pass.

| # | OS | Case and independent assertions |
|---|---|---|
| 1 | both | **Normal value control.** Starting from manual `N`, install `N+1`; old owned PID is dead, exactly one `N+1` bootstrap receipt is valid, actual target files/resources have `N+1` marker, and no duplicate restart occurs. |
| 2 | both | **Identity/capsule escape.** Supply a target whose marker/identity is swapped or redirects outside the capsule. Refuse before mutation; external sentinel, unrelated process, userdata and `N` remain unchanged. |
| 3 | both | **Live N refusal.** With real `N` alive, request install. It may refuse before mutation; it must not mutate target, kill broad processes, or emit success. This is the bounded S0 shutdown contract. |
| 4 | both | **Concurrent install.** Two helpers race for the same transaction target. At most one crosses `before-mutation`; the other changes nothing. There is one lock holder, one commit/restart maximum, and final state is one intact `N` or one verified `N+1`, never mixed. |
| 5 | both | **Wrong receipt.** Run immutable wrong-transaction `N+1`. Even with installer/helper exit 0 or a live child, no success is classified; predecessor is retained and recovery yields manually validated `N`. |
| 6 | both | **Missing receipt.** Run immutable no-receipt `N+1`. Exit 0 or a live child is not success; predecessor is retained and recovery yields manually validated `N`. |
| 7 | macOS | **Kill before mutation.** Kill actual helper at `before-mutation`, then fresh-helper recover. `N` remains whole/runnable; no `N+1` receipt/commit exists. |
| 8 | macOS | **Kill after old → backup.** Kill at `after-old-to-backup`; fresh recovery restores exactly one complete runnable `N`, not a missing/mixed bundle; framework symlinks, executable bytes and modes equal immutable predecessor inventory. |
| 9 | macOS | **Kill after new → target, before commit.** Kill at `after-new-to-target-before-commit`; no receipt means recovery restores and manually boots `N`; no duplicate launch. |
| 10 | Windows | **NSIS target boundary (positive).** Install normal `N+1` to exact spaces/Unicode target. Inspect real installed executable/resource and fixture-owned uninstall/registry values; default/sibling paths stay absent. |
| 11 | Windows | **Real NSIS partial failure.** Run the failure installer. Its external durable witness exists, its required resource is damaged and process exit is nonzero; no success is accepted. Rerun retained `N` installer through fresh recovery and prove normal `N` files, marker, bootstrap and its own registry values are restored. |
| 12 | both | **Recovery idempotency/conservation.** Repeat fresh `recover` after any fault result. State remains one validated `N` (or previously receipt-verified `N+1`), userdata hashes and unrelated sentinel process/files remain unchanged. |

Count matrix: cases 1 and 10 are positive controls; cases 2–9 and 11–12 are
negative, fault, recovery, or concurrency checks: **10/12 (83%)**, above the
70% floor. Wrong and missing receipt are immutable separate fixtures/cases,
not branches merged by an oracle. Every behavioural assertion above executes
when the helper exists; the initial missing-entrypoint assertion is only the
intentional RED bootstrap and cannot be the final coverage mechanism.

All waits have per-stage bounded watchdogs (packaging has its own five-minute
watchdog; app exit/install, preinstall receipt, barrier, helper, installer,
postinstall receipt and recovery are separately bounded) and retain stdout/stderr,
event/receipt files and a capsule inventory on timeout. Teardown only stops
recorded fixture PIDs/process trees and removes its capsule; it never kills by
name, touches shared user data, or alters OS settings.

## Planned test layout and qualification

* `desktop/tests/native-update-probe.test.ts`: package/build/run utilities,
  protocol validation, cases 1–12, opt-in guards, and explicit absent-helper
  RED assertion.
* `desktop/tests/native-update-workflow.test.ts` (only if useful): static
  assertions for the single dedicated workflow and opt-in command.
* `desktop/tests/fixtures/native-update/`: immutable fixture package inputs,
  bootstrap source, resource markers, NSIS custom-install failure input, and
  fixture package harness only.

After macOS packaging, the coordinator can inspect the real generated plist and
start/stop only its created `N` with this self-check (run from `desktop`; it
creates and removes its own temporary capsule):

```sh
node --input-type=module <<'NODE'
import { mkdtemp } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { createCapsule, packageVariant, copyInitialMac, launchFixture, cleanupCapsule } from './tests/fixtures/native-update/tooling.ts';
const capsule = await createCapsule(await mkdtemp(join(tmpdir(), 'native update テスト-')));
try {
  const n = await packageVariant(capsule, 'N', 'mac');
  const plist = await import('node:child_process').then(({ execFileSync }) => execFileSync('/usr/libexec/PlistBuddy', ['-c', 'Print :LSUIElement', join(n.artifact, 'Contents/Info.plist')], { encoding: 'utf8' }));
  if (plist.trim() !== '1') throw new Error(`LSUIElement is ${plist.trim()}`);
  await copyInitialMac(capsule, n.artifact, capsule.target);
  const id = 'selfcheck-N'; const launched = await launchFixture(capsule, join(capsule.target, 'Contents/MacOS', capsule.marker.executableName), { transactionId: id, receiptDir: capsule.receiptDir, userData: capsule.userData, exitFile: join(capsule.receiptDir, `${id}.exit`), bootAttemptFile: join(capsule.receiptDir, `${id}.boot.json`) });
  await launched.stop();
} finally { await cleanupCapsule(capsule); }
NODE
```

The qualification command must select all native cases, fail if none run, and
write Vitest JSON output, for example:

```sh
cd desktop && VOID_NATIVE_UPDATE_PROBE=1 npm test -- --reporter=json \
  --outputFile=../artifacts/native-update/vitest-report.json \
  tests/native-update-probe.test.ts tests/native-update-workflow.test.ts
```

The final implementation may adjust the Vitest JSON-output spelling only after
checking the pinned CLI; it may not weaken this into a successful all-skipped
run. macOS qualification is local arm64 with no UI/Dock; Windows qualification
runs on a dedicated GitHub-hosted `windows-latest` runner, never a shared VM.

## B-owned workflow to add later

B adds **only** `.github/workflows/desktop-update-native-probe.yml`. It runs on
pushes to `work/desktop-update-native` and PRs with relevant path filters, plus
optional `workflow_dispatch`; it has `contents: read`, a bounded job timeout,
and a `macos-14`/`windows-latest` matrix. It checks out, uses Node 22 and the
Go version/toolchain required by `go.mod` (currently Go `1.26.0` /
`go1.26.5`), runs `npm ci --ignore-scripts`, explicitly obtains the pinned
Electron binary if that install mode did not, then runs the opt-in command
above. It always uploads diagnostics/report/logs only—never installers—and
contains no publish, tag, deploy, credentials, or changes to production
packaging/workflows.

## Readiness

The fixture and test contract is finite and implementation-neutral. It remains
provisional until actual native runs prove it: Go helper selection is blocked
until all applicable cases pass, including the Windows retained-`N`-installer
recovery witness. The canonical S0/spec documents at the cited worktree paths
were read and are the controlling constraints for this plan; earlier ENOENT
observations came from the wrong root and are withdrawn.

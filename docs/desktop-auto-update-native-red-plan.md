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
  `go build -o <private-root>/native-update-probe
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
* macOS host arm64 or x64: unsigned `dir` fixture, `identity: null`, `LSUIElement: true`,
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
  "from": {"version": "1.0.0", "target": "/absolute/private/capsule/installed.app", "package": "/…/N-artifact", "predecessorReceipt": "/…/receipts/preinstall-N.json", "predecessorPid": 123},
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
  "pid": 123,
  "packaged": true
}
```

A valid receipt is accepted only after the test independently canonicalizes and
matches every listed expected value, verifies that the PID is the newly
launched owned packaged app, and confirms the old PID is dead. A stale,
malformed, missing, wrong transaction, wrong identity/version/arch/path/marker
receipt is not success.

## A2 executable protocol refinements (controlling test API)

These refinements are test observation/API decisions, not a production layout.
B must not change the tests or fixtures to satisfy its implementation.

* Each manual preinstall and each post-recovery manual witness uses a fresh ID,
  distinct from the update ID. Receipt/config/boot/exit evidence is never erased
  or reused. Recovery alone reuses its install transaction ID. Setup may reinstall
  N between cases, but neither recovery oracle copies a bundle nor runs an installer.
* The fixed helper bootstrap config is
  `receipts/<id>.bootstrap.json`, containing exactly `transactionId`, `receiptDir`,
  `userData` (`<capsule>/userdata`), `exitFile` (`receipts/<id>.exit`) and
  `bootAttemptFile` (`receipts/<id>.boot.json`). Paths are absolute. Launch the
  exact installed executable with `--fixture-bootstrap=<config>`, in a private
  capsule HOME/USERPROFILE/APPDATA/LOCALAPPDATA/TEMP environment. Never inherit
  product credentials or use shared userData. No shell or installer auto-launch.
* Every install stops at every listed durable barrier, in order. The ACK is
  `<id>.<name>.continue`, NOT `<id>.<name>.json.continue`, created by the test via
  temp-file/rename. The helper must actually wait; tests hold each barrier,
  inspect real files and only then ACK. Faults ACK earlier stages and SIGKILL
  only the actual spawned helper ChildProcess at the selected stage.
* On Mac the observation location for the complete retained old bundle is
  `<capsule>/backup.app`. Its symlinks, modes and SHA inventory must equal the
  immutable N bundle at both post-rename barriers. The new target must equal
  the immutable selected N1 variant at the last barrier. This is not a proposed
  production backup path. B's journal remains private and transaction-keyed.
* Recovery is noninteractive: it creates/emits NO install barriers, ignores old
  install ACK state, and launches NO app. It restores N, exits zero, and is then
  independently checked by a fresh manual N launch/receipt/full marker/exit.
  Repeating recovery must again succeed without automatic retry/bootstrap.
* Failed receipt validation / failed NSIS leaves evidence and the retained N
  source for explicit `recover`; do not automatically repair the target before
  A can inspect the failed state. In particular retain the damaged Windows
  marker and 1.0.1 registry registration after the partial installer failure.
  The helper must wait for and reject the real nonzero installer result. A's
  external customInstall witness plus the damaged target/registry is the fault
  oracle, not a helper's self-reported installer status.
* A owns the fixture exit-file protocol, including for B-launched wrong-token
  and normal apps. Do not kill them on a receipt timeout or replace their live
  target. Missing-receipt mode exits naturally. A closes witnessed bad boots
  before recovery. Helpers must release the target lock on exit; a refused
  contender must not disturb the held first transaction, which must then finish
  a real N1 install. Recovery never claims a live recorded successor as dead.
* Windows registry checks query individual values from the fixture GUID-derived
  **HKCU install key** (`registryWitness.installKey`) for `InstallLocation`, equal
  to the exact target, and **HKCU uninstall key** (`uninstallKey`) for
  `DisplayVersion`, equal to the actual expected version (pinned installer.nsh
  lines 103–128). No substring/combined
  output oracle. Retained N installer SHA is invariant. Reinstalled N's app
  inventory must match the preinstall inventory (only generated `Uninstall `
  entries are excluded); bootstrap and registration are independently required.
* Native helper operation/barrier/PID waits are 45 seconds; tracked helper
  subprocesses have a 180-second outer watchdog to include barrier observation,
  and are killed/reaped on failure. Build has 120 seconds, fixture packaging
  uses two safe waves of isolated variant projects, each with its existing
  five-minute watchdog. The first native case allows setup plus operations
  (16 minutes), not the rejected 75-second bound. Subsequent cases reuse the
  built packages. This setup ceiling is approximately 12 minutes including Go,
  not a claim of a measured ten-minute Windows package time.
* All captured helper/tool children are bounded and reaped. B-owned fixture
  processes are stopped only through witnessed exit files and actual PID
  disappearance, never PID signals or name-based termination. After uncertainty,
  further setup is refused and evidence is retained. Capsules and external
  sentinels are conservatively retained even on a passing observed run;
  teardown does not call destructive fixture cleanup. CI copies
  only diagnostic `.log/.json/.txt` files into `artifacts/native-update/<host>`.
  The fixture GUID registry keys remain disposable runner residue. Retention
  never excuses lingering witnessed children: cleanup failures fail the suite.
  No installers are uploaded.

### Immutable bootstrap ledger and exact observation scope

Fixture-author commit `a430021` closes the overwritten-witness gap. `main.mjs`
first writes `${bootAttemptFile}.attempt-${process.pid}-${randomUUID()}.json`,
then the canonical `bootAttemptFile`. Both have the same BootAttempt schema:
`{v:1,transactionId,pid,execPath,resourcesPath,packaged:true,mode}`. The fixture
README and tests use this actual naming contract; B must not manufacture records.

Tests enumerate the complete immutable ledger, require exactly one update attempt
on success, correlate its actual PID with the 1.0.1 receipt and full resource
marker, and compare the ledger across forbidden boots, contention, missing receipt
and repeated recovery. Version/marker come from the real receipt/resources, not
invented BootAttempt fields or fallbacks. Cleanup validates each transaction's
records and paths/packaged/PIDs, creates one transaction-owned exit file, and waits
for every validated witnessed PID to disappear without signalling receipt PIDs.
The canonical file remains a compatibility witness, not the counting oracle.

This observes packaged bootstraps reaching the fixture ledger, within bounded
native test windows; it does not prove processes which fail before that write,
indefinite future behaviour, or full product updater correctness. No commands or
native runs were performed for this final test-only refinement. Native S0 remains
unqualified until actual macOS and Windows runs pass. The independent entrypoint
existence assertion records pure missing-helper RED; any build, packaging or
lifecycle failure after existence is a separate failure, never bootstrap RED.

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

The table above is the acceptance scope, not the numbering of the A2 file.
A2 has **12 named native behaviors**: one shared success/path/registry control;
separate shared identity, real redirect, live-N, lock-contention, wrong-receipt
and missing-receipt cases; three Mac crash points; Windows partial NSIS failure;
and shared interrupted recovery/idempotency. That is **11/12 (92%)** negative,
fault, recovery or concurrency behaviors, **11 applicable on Mac / 9 on Windows**.
Path/registry positive coverage is merged into the normal control. Conservation
and independent repeated manual recovery witnesses are also checked in every
fault case, not postponed to an inventory-only final test. Wrong and missing receipt are immutable separate fixtures/cases,
not branches merged by an oracle. Every behavioural assertion above executes
when the helper exists; the initial missing-entrypoint assertion is only the
intentional RED bootstrap and cannot be the final coverage mechanism.

Waits have native bounded watchdogs; packaging retains its own five-minute
per-variant bound. Helpers, installer tooling, receipt/barrier/PID waits retain
stdout/stderr and real event/receipt files on failure. Teardown never kills by
name, touches shared user data, or alters OS settings. A2 retention/ownership
rules and the bounded immutable-ledger observation scope are specified above.

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
node --experimental-strip-types --input-type=module <<'NODE'
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
`go1.26.5`), runs `npm ci --ignore-scripts`, then explicitly executes
`node node_modules/electron/install.js` from `desktop` (Electron `41.10.3`
is pinned in package.json/lockfile). It runs the opt-in test command with
`VOID_NATIVE_UPDATE_PROBE: '1'` in the step/job environment, so both shells
actually execute it, not a POSIX-only assignment on Windows. Checkout sets
`persist-credentials: false`. The dedicated single matrix job has a 10–60
minute timeout; actions use the repository's pinned checkout/setup-node/setup-go/
upload-artifact SHAs. PR filters cover this workflow, Go manifests, desktop
manifests, helper, both tests, fixtures and the plan. Upload paths must explicitly
select diagnostic file extensions under `artifacts/native-update/`, not an entire
capsule/directory that might include installers. It always uploads diagnostics/report/logs only—never installers—and
contains no publish, tag, deploy, credentials, or changes to production
packaging/workflows.

## Readiness

The fixture and test contract is finite and implementation-neutral. It remains
provisional until actual native runs prove it: Go helper selection is blocked
until all applicable cases pass, including the Windows retained-`N`-installer
recovery witness. The canonical S0/spec documents at the cited worktree paths
were read and are the controlling constraints for this plan; earlier ENOENT
observations came from the wrong root and are withdrawn.

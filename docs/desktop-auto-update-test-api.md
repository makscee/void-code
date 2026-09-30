# Desktop update R1–R5 test API

This is a **test-chosen callable contract**, not production wiring.

- `desktop/src/main/desktop-update-trust.ts` exports:
  - `verifyDesktopUpdateEnvelope(raw, keyring)`, returning `{ ok: true, verified }` or `{ ok: false, code }`.
  - `parseAndSelectDesktopRelease(payloadBytes, installed)`, returning `{ ok: true, plan }` or `{ ok: false, code }`.
- `desktop/src/main/desktop-update-controller.ts` exports
  `createDesktopUpdateController(dependencies)`. Its public controller has only
  `check`, `download`, `cancelDownload`, `requestInstall`, `dispose`, and `snapshot`.
  There is intentionally no public `confirmInstall`.

Exact TypeScript shapes are owned by `desktop/tests/desktop-update-fixtures.ts`.
The trust verifier authenticates the original decoded payload bytes, before payload
UTF-8/JSON parsing. Envelope and payload objects have strict, duplicate-free shapes.

The controller keeps metadata and manifest payloads bounded in memory. `download`
returns an opaque `StageHandle` (`{ id: string }`), not archive bytes: the staged file
may be as large as 1 GiB and must remain behind the injected stage boundary.
`verifyArtifact`, `prepare`, `reverify`, and `handoff` receive that handle and the
pinned metadata plan. `verifyArtifact` checks the initial staged file against the
plan's length and SHA-256; `reverify` checks those same bytes immediately before
handoff. Snapshots must not reveal a plan URL/path/key, payload, or staged bytes.

The minimal R3 store is injected as `loadFloor`/`saveFloor`; the controller validates
store values itself, compares and saves a verified floor before `available`, and fails
closed on corrupt or unavailable storage. A real durable filesystem store is outside
this slice.

`showNativeInstallDialog` is a main-process evidence seam only. `prepare` is
non-destructive preflight, and `handoff` transfers to a future helper—it is not product
update success. Required install order is native consent, prepare, reverify, handoff.

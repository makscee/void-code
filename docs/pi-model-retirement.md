# Managed Pi model retirement and migration

VC owns the `void-codex` catalog it registers with Pi. Retiring a model is a compatibility change, not a string replacement: new selectors must stop advertising the retired ID while existing settings and append-only sessions must select a supported successor automatically.

## Current generation

The IDs were confirmed against OpenAI's model catalog before implementation:

- GPT-6 Sol: `gpt-6-sol` — <https://developers.openai.com/api/docs/models/gpt-6-sol>
- GPT-6 Luna: `gpt-6-luna` — <https://developers.openai.com/api/docs/models/gpt-6-luna>

The VC migration table is:

| Retired ID | Successor ID | Reason |
|---|---|---|
| `gpt-5.6-sol` | `gpt-6-sol` | same product tier |
| `gpt-5.6-terra` | `gpt-6-sol` | Sol is the safe general-purpose/default successor |
| `gpt-5.6-luna` | `gpt-6-luna` | same product tier |

`gpt-6-astra` remains available and is not part of this retirement.

## Runtime behavior

Two explicit copies of the table are intentionally kept at the language boundary and contract-tested for equality:

- `piModelRetirements` in `cmd/vc/pi_settings.go` migrates only an exact `defaultProvider: "void-codex"` plus retired `defaultModel` pair in global settings and an existing project `.pi/settings.json`. Foreign providers, provider-less model strings, missing project files, and unrelated settings are preserved. The existing locked atomic settings writer supplies malformed/read-only diagnostics.
- `MODEL_RETIREMENTS` in the embedded managed extension (`cmd/vc/pi_extension.go`) follows Pi's effective active-branch ordering: both `model_change` entries and later assistant messages can select the current model. On startup, reload, or in-process resume it uses Pi's normal `setModel` API to append the successor. Because that API also writes global defaults, VC snapshots and compare-and-restores those two keys through its locked writer, normalizing a retired VC snapshot but retaining foreign/current defaults. Append-only session history is preserved; once the successor is latest, another run is a no-op.

Both terminal VC and packaged desktop install the same extension and call the same global/project settings reconciler after access admission. When Pi loads the managed extension directly, `pi-bootstrap` persists an exact retired global default and returns its successor as a one-startup model-ordering hint; this prevents Pi's already-cached retired Luna setting from falling back to Sol. Desktop resumes pass the discovered session to `vc desktop-session`; Pi's `session_start` hook performs the session migration. This also covers a session selected later through Pi's native resume UI.

Fresh catalog surfaces are maintained in `piVoidCodexModels`, the embedded extension allowlist/names, and the managed web-search candidate list. Retired IDs belong only in migration tables and upgrade fixtures.

## Procedure for a future retirement

1. Confirm exact successor IDs from the provider's current catalog/protocol. Do not infer names.
2. Add each old-to-new row to both retirement tables. Choose and document a successor for every VC-advertised old variant.
3. Add successors and remove retired IDs in every fresh catalog surface listed above. Move the fresh default if needed.
4. Update the pinned-Pi upgrade smoke with a real retired session entry. It must prove the resumed model, preserved message history, fresh catalog, retired absence, and repeated-run idempotence without `/model`.
5. Run focused Go tests, the full fixture-tagged Go suite, desktop Vitest/lint/typecheck/build, and the pinned Pi smoke. Review the exact head independently.
6. Release the VC CLI and packaged desktop from the same accepted commit. No server deployment is required for this client catalog change, but the relay/provider grant must already accept the successor IDs.

## Rollback

Reverting only the fresh catalog is unsafe after clients have appended successor selections. A rollback build must continue to register every successor that an upgraded client may have persisted, or add reverse aliases as a new reviewed migration. Prefer fixing forward. Do not delete or rewrite historical session records. Merge, tag, release publication, desktop rollout, and any relay change each retain their normal operator gates.

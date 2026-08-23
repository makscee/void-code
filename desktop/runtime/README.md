# Private resource inputs

`npm run assemble` is intentionally offline. It reconstructs the Pi dependency tree with `npm ci --offline` from `pi/package-lock.json`; every tarball therefore must already exist in npm's configured cache (normally populated once by `npm ci` in `runtime/pi` while online). Missing cache entries fail assembly rather than changing the lock or fetching unpinned input.

Node is assembled only from the official macOS-arm64 archive pinned by `source` and `sourceArchiveSha256` in `scripts/resource-pins.json`. The deterministic local cache path is `runtime/cache/node/<archive filename from source>`; this ignored cache is the sole Node input to assembly. Populate it explicitly outside the offline check:

```sh
npm run setup
```

That runs `scripts/fetch-pinned-node.mjs`, which reads the URL and the expected
SHA-256 from `scripts/resource-pins.json`; neither value is duplicated anywhere
else. Downloaded bytes are staged beside the cache and only moved into it once
their digest matches, so a rejected download cannot leave the cache poisoned.
`provision-pinned-pi-smoke.sh` calls the same command.

Assembly requires the source identifier to exactly match Node's versioned HTTPS distribution URL, hashes the archive before opening it, rejects unsafe or unexpected layouts, extracts only `node-v22.23.1-darwin-arm64/bin/node` into a temporary directory, then verifies that executable's version and SHA-256 before staging it. Missing or changed cache input fails without a network request. An arbitrary executable path is not an assembly input.

The Pi package file, lock file, locked top-level registry integrity, and reconstructed tree hash are pinned in `scripts/resource-pins.json`. Existing `runtime/pi/node_modules` contents are ignored. npm's generated `node_modules/.package-lock.json` is removed because it describes the already pinned source lock and varies by npm version.

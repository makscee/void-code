#!/usr/bin/env node
// Release-pin qualification: compares the pinned digest with what the release publishes.
// Touches the network on purpose, so it is a command rather than part of `npm test`.
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { assertPinMatchesPublishedAsset } from './verify-windows-pin-lib.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const pin = JSON.parse(await readFile(path.join(here, 'resource-pins.json'), 'utf8')).windows.vc;

/**
 * Release assets of a public repository are plain HTTPS downloads. Fetching them
 * directly keeps this runnable on any machine — no CLI to install, no token to
 * hold — which matters because the check exists to be run by whoever doubts the pin.
 */
async function fetchPublishedSums({ repository, releaseTag }) {
  const url = `https://github.com/${repository}/releases/download/${releaseTag}/SHA256SUMS`;
  const response = await fetch(url, { redirect: 'follow' });
  if (!response.ok) {
    throw new Error(`cannot read published checksums: ${url} returned ${response.status} ${response.statusText}`);
  }
  return response.text();
}

await assertPinMatchesPublishedAsset(pin, fetchPublishedSums);
console.log(`ok: ${pin.assetName} of ${pin.repository} ${pin.releaseTag} matches the pinned digest`);

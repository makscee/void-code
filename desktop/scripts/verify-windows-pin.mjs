#!/usr/bin/env node
// Release-pin qualification: compares the pinned digest with what the release publishes.
// Touches the network on purpose, so it is a command rather than part of `npm test`.
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { assertPinMatchesPublishedAsset, fetchPublishedSums } from './verify-windows-pin-lib.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const pin = JSON.parse(await readFile(path.join(here, 'resource-pins.json'), 'utf8')).windows.vc;

await assertPinMatchesPublishedAsset(pin, fetchPublishedSums);
console.log(`ok: ${pin.assetName} of ${pin.repository} ${pin.releaseTag} matches the pinned digest`);

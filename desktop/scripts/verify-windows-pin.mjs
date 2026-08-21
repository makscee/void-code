#!/usr/bin/env node
// Release-pin qualification: compares the pinned digest with what the release publishes.
// Touches the network on purpose, so it is a command rather than part of `npm test`.
import { execFileSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { assertPinMatchesPublishedAsset } from './verify-windows-pin-lib.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const pin = JSON.parse(await readFile(path.join(here, 'resource-pins.json'), 'utf8')).windows.vc;

await assertPinMatchesPublishedAsset(pin, ({ repository, releaseTag }) =>
  execFileSync('gh', ['release', 'view', releaseTag, '--repo', repository, '--json', 'assets', '--jq',
    '.assets[] | select(.name=="SHA256SUMS") | .url'], { encoding: 'utf8' }).trim()
    ? execFileSync('gh', ['release', 'download', releaseTag, '--repo', repository, '--pattern', 'SHA256SUMS', '--output', '-'], { encoding: 'utf8' })
    : '');
console.log(`ok: ${pin.assetName} of ${pin.repository} ${pin.releaseTag} matches the pinned digest`);

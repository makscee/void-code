#!/usr/bin/env node
// Thin CLI over ensurePinnedNode: reads the pin, downloads with curl, reports.
import { execFileSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { ensurePinnedNode } from './fetch-pinned-node-lib.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const pins = JSON.parse(await readFile(path.join(here, 'resource-pins.json'), 'utf8'));

const result = await ensurePinnedNode({
  pins: pins.node,
  cacheDir: path.join(here, '..', 'runtime/cache/node'),
  download: (url, destination) =>
    execFileSync('curl', ['--fail', '--location', '--proto', '=https', '--tlsv1.2', url, '--output', destination], { stdio: 'inherit' }),
});
console.log(`${result.action}: ${result.archive}`);

#!/usr/bin/env node
// Thin CLI over ensurePinnedNode: reads the pin, downloads with curl, reports.
import { execFileSync } from 'node:child_process';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { ensurePinnedNode } from './fetch-pinned-node-lib.mjs';
import { nodePinFor } from './resource-assembly-lib.mjs';

const here = path.dirname(fileURLToPath(import.meta.url));
const pins = JSON.parse(await readFile(path.join(here, 'resource-pins.json'), 'utf8'));
// The cache holds one archive per platform, named after the pin's own source
// URL, so archives sit beside each other rather than overwriting one another.
//
// A build aimed at another architecture needs both: the one it ships, and the
// one this machine can run to reconstruct Pi with the pinned npm. Asking for a
// target is therefore asking for both, and asking for nothing is asking for
// this machine's alone.
const host = `${process.platform}-${process.arch}`;
const wanted = [...new Set([host, `${process.platform}-${process.env.VOID_DESKTOP_MAC_ARCH ?? process.arch}`])];

for (const platform of wanted) {
  const result = await ensurePinnedNode({
    pins: nodePinFor(pins, platform),
    cacheDir: path.join(here, '..', 'runtime/cache/node'),
    download: (url, destination) =>
      execFileSync('curl', ['--fail', '--location', '--proto', '=https', '--tlsv1.2', url, '--output', destination], { stdio: 'inherit' }),
  });
  console.log(`${result.action}: ${result.archive}`);
}

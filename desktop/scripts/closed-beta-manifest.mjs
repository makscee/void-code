#!/usr/bin/env node
import { readFileSync, writeFileSync } from 'node:fs';
import { assembleClosedBetaEnvelope, buildClosedBetaPayload, payloadSha256 } from './closed-beta-manifest-lib.mjs';

function usage() { throw new Error('usage: closed-beta-manifest.mjs payload --input <public-json> --out <new-payload> | envelope --payload <payload> --signature <detached-signature> --key-id <id> --out <new-envelope>'); }
function args(values) {
  const result = Object.create(null);
  for (let index = 0; index < values.length; index += 2) {
    const key = values[index]; const value = values[index + 1];
    if (!key?.startsWith('--') || !value || Object.hasOwn(result, key)) usage();
    result[key] = value;
  }
  return result;
}
function writeNew(path, bytes) { writeFileSync(path, bytes, { encoding: undefined, flag: 'wx', mode: 0o600 }); }

const [command, ...rest] = process.argv.slice(2);
try {
  const options = args(rest);
  if (command === 'payload' && Object.keys(options).sort().join(',') === '--input,--out') {
    const input = JSON.parse(readFileSync(options['--input'], 'utf8'));
    const payload = buildClosedBetaPayload(input);
    writeNew(options['--out'], payload);
    process.stdout.write(`payload-sha256=${payloadSha256(payload)}\n`);
  } else if (command === 'envelope' && Object.keys(options).sort().join(',') === '--key-id,--out,--payload,--signature') {
    const payload = readFileSync(options['--payload']);
    const signature = readFileSync(options['--signature']);
    const envelope = assembleClosedBetaEnvelope(payload, signature, options['--key-id']);
    writeNew(options['--out'], envelope);
    process.stdout.write(`payload-sha256=${payloadSha256(payload)}\n`);
  } else usage();
} catch {
  process.stderr.write('closed-beta artifact operation rejected\n');
  process.exitCode = 1;
}

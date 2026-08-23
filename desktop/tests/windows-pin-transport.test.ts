import { readFileSync } from 'node:fs';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { fetchPublishedSums } from '../scripts/verify-windows-pin-lib.mjs';

/**
 * The comparison logic was covered from the start; the transport was the seam
 * left outside, and the transport is what broke in review. These exercise the
 * three outcomes a network call actually has — an answer, a failure, and
 * silence — and the last one is the one a bare retry loop misses.
 */
const pin = { repository: 'makscee/void-code', releaseTag: 'v0.2.47', assetName: 'vc-windows-amd64.exe' };
const nap = () => Promise.resolve();

function responder(...outcomes: Array<number | 'hang' | 'throw'>) {
  const calls: string[] = [];
  const bodiesCancelled: number[] = [];
  const fetchImpl = (url: string, init: { signal: AbortSignal }) => {
    const outcome = outcomes[Math.min(calls.length, outcomes.length - 1)];
    calls.push(url);
    if (outcome === 'throw') return Promise.reject(new TypeError('fetch failed'));
    if (outcome === 'hang') {
      // Answers nothing and fails nothing until the attempt's own deadline fires.
      return new Promise((_, reject) => {
        init.signal.addEventListener('abort', () => reject(init.signal.reason), { once: true });
      });
    }
    const index = calls.length;
    return Promise.resolve({
      ok: outcome === 200,
      status: outcome,
      statusText: String(outcome),
      text: () => Promise.resolve('digest  vc-windows-amd64.exe'),
      body: { cancel: () => { bodiesCancelled.push(index); return Promise.resolve(); } },
    });
  };
  return { fetchImpl, calls, bodiesCancelled };
}

describe('published checksum transport', () => {
  it('returns the body on the first answer', async () => {
    const { fetchImpl, calls } = responder(200);
    await expect(fetchPublishedSums(pin, { fetchImpl, sleep: nap })).resolves.toContain('vc-windows-amd64.exe');
    expect(calls).toHaveLength(1);
  });

  it('does not retry a 404: a missing release is an answer, not a hiccup', async () => {
    const { fetchImpl, calls } = responder(404);
    await expect(fetchPublishedSums(pin, { fetchImpl, sleep: nap })).rejects.toThrow(/cannot read published checksums/);
    expect(calls).toHaveLength(1);
  });

  it('retries a 503 and succeeds when the upstream recovers', async () => {
    const { fetchImpl, calls } = responder(503, 503, 200);
    await expect(fetchPublishedSums(pin, { fetchImpl, sleep: nap })).resolves.toContain('vc-windows-amd64.exe');
    expect(calls).toHaveLength(3);
  });

  it('releases the body of a 5xx instead of leaving the socket held', async () => {
    const { fetchImpl, bodiesCancelled } = responder(503, 200);
    await fetchPublishedSums(pin, { fetchImpl, sleep: nap });
    expect(bodiesCancelled).toEqual([1]);
  });

  it('retries a thrown network error', async () => {
    const { fetchImpl, calls } = responder('throw', 'throw', 200);
    await expect(fetchPublishedSums(pin, { fetchImpl, sleep: nap })).resolves.toBeTruthy();
    expect(calls).toHaveLength(3);
  });

  it('aborts a hung attempt on its own deadline and moves to the next', async () => {
    const { fetchImpl, calls } = responder('hang', 200);
    await expect(fetchPublishedSums(pin, { fetchImpl, sleep: nap, timeoutMs: 25 })).resolves.toBeTruthy();
    expect(calls).toHaveLength(2);
  });

  it('gives up after the last attempt with the transport reason, not a verdict', async () => {
    const { fetchImpl, calls } = responder('hang');
    await expect(fetchPublishedSums(pin, { fetchImpl, sleep: nap, timeoutMs: 25 }))
      .rejects.toThrow(/after 3 attempts/);
    expect(calls).toHaveLength(3);
  });
});

describe('developer-facing contract', () => {
  const desktop = path.resolve(__dirname, '..');

  it('registers the command the README tells people to run', () => {
    const { scripts } = JSON.parse(readFileSync(path.join(desktop, 'package.json'), 'utf8'));
    expect(scripts['qualify:windows-pin']).toBe('node scripts/verify-windows-pin.mjs');
  });

  it('needs no CLI to be installed: the qualification shells out to nothing', () => {
    const source = readFileSync(path.join(desktop, 'scripts/verify-windows-pin.mjs'), 'utf8')
      + readFileSync(path.join(desktop, 'scripts/verify-windows-pin-lib.mjs'), 'utf8');
    expect(source).not.toMatch(/child_process|execFile|spawn|\bgh\b/);
  });
});

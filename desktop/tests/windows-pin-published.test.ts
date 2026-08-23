import { describe, expect, it } from 'vitest';
import { assertPinMatchesPublishedAsset } from '../scripts/verify-windows-pin-lib.mjs';

/**
 * The unit suite can only check that the pin is internally consistent. Whether
 * the digest belongs to the asset it names lives on GitHub, so the comparison
 * is injected here and performed for real by scripts/verify-windows-pin.mjs.
 *
 * Without this, a pin may name a release and carry a digest from anywhere —
 * which is precisely the state the provenance work was undoing.
 */
const pin = {
  repository: 'makscee/void-code',
  releaseTag: 'v0.2.47',
  assetName: 'vc-windows-amd64.exe',
  sha256: '00ae01d69475460a3234f8bc2c26121a396c0b7b01bb43c58510c8061cc2f15b',
};

const SUMS = [
  '1a008fd2e1b724fa31276cea64110dd5d1be0a27d7aa69370ec47555871a133a  vc-linux-amd64',
  '00ae01d69475460a3234f8bc2c26121a396c0b7b01bb43c58510c8061cc2f15b  vc-windows-amd64.exe',
  '5a085da77c94db933c5b86f6af64b2934b4e87190393da92e02218e6c40072ad  vc-windows-arm64.exe',
].join('\n');

describe('windows pin against published checksums', () => {
  it('accepts a digest that the release publishes for that asset', async () => {
    await expect(assertPinMatchesPublishedAsset(pin, async () => SUMS)).resolves.toBeUndefined();
  });

  it('rejects a digest the release does not publish', async () => {
    const stale = { ...pin, sha256: '5b8de963faf518135758b39f4608ca3ecc53b2f994b63154b0a034f4f0479f9f' };
    await expect(assertPinMatchesPublishedAsset(stale, async () => SUMS)).rejects.toThrow(/does not match/i);
  });

  it('rejects a digest published for a different asset of the same release', async () => {
    const swapped = { ...pin, sha256: '5a085da77c94db933c5b86f6af64b2934b4e87190393da92e02218e6c40072ad' };
    await expect(assertPinMatchesPublishedAsset(swapped, async () => SUMS)).rejects.toThrow(/does not match/i);
  });

  it('refuses when the release publishes no such asset', async () => {
    const missing = { ...pin, assetName: 'vc-windows-riscv.exe' };
    await expect(assertPinMatchesPublishedAsset(missing, async () => SUMS)).rejects.toThrow(/publishes no/i);
  });
});

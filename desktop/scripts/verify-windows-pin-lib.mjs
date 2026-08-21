/**
 * Check that the Windows pin's digest is the one the named release publishes
 * for the named asset.
 *
 * The release's SHA256SUMS is the cheapest authority available: a few hundred
 * bytes instead of a nine-megabyte download, published by the same workflow that
 * built the binaries. `fetchSums(pin)` is injected so the comparison itself can
 * be exercised without a network.
 */
export async function assertPinMatchesPublishedAsset(pin, fetchSums) {
  const sums = await fetchSums(pin);
  const published = new Map(
    sums
      .split('\n')
      .map((line) => line.trim().split(/\s+/))
      .filter((parts) => parts.length === 2)
      .map(([digest, name]) => [name, digest]),
  );

  const expected = published.get(pin.assetName);
  if (expected === undefined) {
    throw new Error(
      `${pin.repository} ${pin.releaseTag} publishes no ${pin.assetName}; it publishes ${[...published.keys()].join(', ')}`,
    );
  }
  if (expected !== pin.sha256) {
    throw new Error(
      `pinned digest does not match the published ${pin.assetName} of ${pin.repository} ${pin.releaseTag}: pin says ${pin.sha256}, the release publishes ${expected}`,
    );
  }
}

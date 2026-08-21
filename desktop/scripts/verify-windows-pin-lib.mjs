const sleepDefault = (ms) => new Promise((resolve) => { setTimeout(resolve, ms); });

/**
 * Read the checksums a release published, retrying the transport and nothing else.
 *
 * Three outcomes have to stay distinguishable, because two of them are answers
 * and one is noise: a permanent response (4xx) is an answer about the pin, a 5xx
 * or network error is noise worth retrying, and silence — a connection that
 * neither answers nor fails — is the case a bare retry loop misses entirely.
 * Each attempt therefore carries its own deadline.
 *
 * `fetchImpl` and `sleep` are injected so this is exercised without a network.
 */
export async function fetchPublishedSums(pin, options = {}) {
  const { fetchImpl = fetch, attempts = 3, timeoutMs = 15_000, sleep = sleepDefault } = options;
  const url = `https://github.com/${pin.repository}/releases/download/${pin.releaseTag}/SHA256SUMS`;
  let last;

  for (let attempt = 1; attempt <= attempts; attempt += 1) {
    try {
      const response = await fetchImpl(url, { redirect: 'follow', signal: AbortSignal.timeout(timeoutMs) });
      if (response.ok) return await response.text();
      if (response.status < 500) {
        const permanent = new Error(`cannot read published checksums: ${url} returned ${response.status} ${response.statusText}`);
        permanent.permanent = true;
        throw permanent;
      }
      // Nothing here reads the body, and an unread one holds the socket open.
      await response.body?.cancel();
      last = new Error(`${url} returned ${response.status} ${response.statusText}`);
    } catch (error) {
      if (error.permanent) throw error;
      last = error;
    }
    if (attempt < attempts) await sleep(attempt * 1000);
  }
  throw new Error(`cannot read published checksums after ${attempts} attempts: ${last.message}`);
}

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

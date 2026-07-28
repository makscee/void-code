export function normalizeAsarEntry(entry) {
  return entry.replaceAll('\\', '/');
}

export function startupFailureTimeoutMs(windows) {
  return windows ? 600_000 : 20_000;
}

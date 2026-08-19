import { load as loadYaml } from 'js-yaml';

export function normalizeAsarEntry(entry) {
  return entry.replaceAll('\\', '/');
}

export function startupFailureTimeoutMs(windows) {
  return windows ? 600_000 : 20_000;
}

function yamlObject(text, label) {
  const value = loadYaml(text);
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error(`${label} must be an object`);
  return value;
}

export function parseAppUpdateYaml(text) {
  const value = yamlObject(text, 'app-update.yml');
  const raw = typeof value.publisherName === 'string' ? [value.publisherName] : value.publisherName;
  if (value.provider !== 'generic' || value.url !== 'https://vc.makscee.ru/download/windows/') throw new Error('app-update.yml generic feed mismatch');
  if (!Array.isArray(raw) || raw.length !== 1 || typeof raw[0] !== 'string' || !raw[0] || raw[0].trim() !== raw[0]) throw new Error('app-update.yml signed publisher missing or ambiguous');
  return { provider: value.provider, url: value.url, publisherNames: [raw[0]] };
}

export function assertLocalizationEntries(entries) {
  const normalized = new Set(entries.map(normalizeAsarEntry));
  for (const required of ['/dist/renderer/l10n/en.json', '/dist/renderer/l10n/ru.json', '/dist/THIRD_PARTY_NOTICES.md']) {
    if (!normalized.has(required)) throw new Error(`packaged localization resource missing: ${required}`);
  }
}

export function assertWindowsUpdaterMetadata({ appUpdateText, latestText, version, installerName, size, sha512, expectedPublisherName }) {
  const appUpdate = parseAppUpdateYaml(appUpdateText); const latest = yamlObject(latestText, 'latest.yml');
  if (typeof expectedPublisherName !== 'string' || !expectedPublisherName || expectedPublisherName.trim() !== expectedPublisherName || appUpdate.publisherNames[0] !== expectedPublisherName) throw new Error('app-update.yml signed publisher mismatched expected signer');
  if (!Array.isArray(latest.files) || latest.files.length !== 1) throw new Error('latest.yml full installer identity mismatch');
  const file = latest.files[0];
  if (!file || typeof file !== 'object' || Array.isArray(file) || file.url !== installerName || file.sha512 !== sha512 || latest.version !== version || latest.path !== installerName || latest.sha512 !== sha512) throw new Error('latest.yml full installer identity mismatch');
  if (file.size !== size || (latest.size !== undefined && latest.size !== size)) throw new Error('latest.yml installer size mismatch');
}

import { randomUUID, createHash } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const API = 'https://api.github.com';
const UPLOADS = 'https://uploads.github.com';

export class OwnedReleasePublisher {
  constructor({ fetchImpl = fetch, token, repository, tag, distDir, expectedFile, prerelease, nonce = randomUUID() }) {
    this.fetch = fetchImpl;
    this.token = token;
    this.repository = repository;
    this.tag = tag;
    this.distDir = distDir;
    this.expectedFile = expectedFile;
    this.prerelease = prerelease;
    this.marker = `void-code-release-owner:${nonce}`;
    this.releaseId = null;
  }

  headers(extra = {}) {
    return { Accept: 'application/vnd.github+json', Authorization: `Bearer ${this.token}`, 'X-GitHub-Api-Version': '2022-11-28', ...extra };
  }

  async request(url, { expected, label, ...init }) {
    let response;
    try { response = await this.fetch(url, { ...init, headers: this.headers(init.headers) }); }
    catch (error) { throw new Error(`${label} network failure: ${error instanceof Error ? error.message : String(error)}`); }
    if (!expected.includes(response.status)) throw new Error(`${label} failed with HTTP ${response.status}`);
    if (response.status === 204 || response.status === 404) return { response, body: null };
    let body;
    try { body = await response.json(); }
    catch { throw new Error(`${label} returned invalid JSON`); }
    return { response, body };
  }

  async assertAbsent() {
    const byTag = await this.request(`${API}/repos/${this.repository}/releases/tags/${encodeURIComponent(this.tag)}`, { expected: [200, 404], label: 'release lookup' });
    if (byTag.response.status === 200) throw new Error(`release already exists for ${this.tag}`);

    let url = `${API}/repos/${this.repository}/releases?per_page=100`;
    while (url) {
      const page = await this.request(url, { expected: [200], label: 'draft enumeration' });
      if (!Array.isArray(page.body)) throw new Error('draft enumeration returned a non-list');
      if (page.body.some((release) => release?.tag_name === this.tag)) throw new Error(`release already exists for ${this.tag}`);
      const link = page.response.headers.get('link') ?? '';
      const next = link.split(',').map((part) => part.trim()).find((part) => /rel="next"/.test(part));
      url = next?.match(/^<([^>]+)>/)?.[1] ?? '';
    }
  }

  assertOwned(release, { draft = true } = {}) {
    if (!release || release.id !== this.releaseId || release.tag_name !== this.tag || release.draft !== draft || typeof release.body !== 'string' || !release.body.includes(this.marker) || typeof release.name !== 'string' || !release.name.includes(this.marker)) {
      throw new Error(`release identity/ownership mismatch for release_id=${this.releaseId ?? 'unassigned'}`);
    }
    return release;
  }

  async create() {
    const result = await this.request(`${API}/repos/${this.repository}/releases`, {
      method: 'POST', expected: [201], label: 'draft create',
      body: JSON.stringify({ tag_name: this.tag, name: `${this.tag} ${this.marker}`, body: this.marker, draft: true, prerelease: this.prerelease }),
      headers: { 'content-type': 'application/json' },
    });
    if (!Number.isSafeInteger(result.body?.id)) throw new Error('draft create returned invalid release identity');
    this.releaseId = result.body.id;
    this.assertOwned(result.body);
    const exactUploadPrefix = `${UPLOADS}/repos/${this.repository}/releases/${this.releaseId}/assets`;
    if (typeof result.body.upload_url !== 'string' || !result.body.upload_url.startsWith(exactUploadPrefix)) throw new Error('draft create returned wrong release upload identity');
  }

  async readOwned(draft = true) {
    const result = await this.request(`${API}/repos/${this.repository}/releases/${this.releaseId}`, { expected: [200], label: 'owned release read' });
    return this.assertOwned(result.body, { draft });
  }

  async expectedAssets() {
    const names = (await readFile(this.expectedFile, 'utf8')).split(/\r?\n/).filter(Boolean);
    if (names.length === 0 || names.some((name) => name !== path.basename(name)) || new Set(names).size !== names.length) throw new Error('invalid expected asset manifest');
    return names.slice().sort();
  }

  async upload(name) {
    await this.readOwned(true);
    const bytes = await readFile(path.join(this.distDir, name));
    await this.request(`${UPLOADS}/repos/${this.repository}/releases/${this.releaseId}/assets?name=${encodeURIComponent(name)}`, {
      method: 'POST', expected: [201], label: `asset upload ${name}`, body: bytes,
      headers: { 'content-type': 'application/octet-stream', 'content-length': String(bytes.length) },
    });
  }

  async validateAssets(release, names) {
    if (!Array.isArray(release.assets)) throw new Error('release assets missing');
    const remoteNames = release.assets.map((asset) => asset?.name).sort();
    if (JSON.stringify(remoteNames) !== JSON.stringify(names)) throw new Error('release asset manifest mismatch');
    for (const name of names) {
      const asset = release.assets.find((candidate) => candidate?.name === name);
      if (typeof asset?.digest !== 'string' || !/^sha256:[0-9a-f]{64}$/.test(asset.digest)) throw new Error(`release asset digest unavailable for ${name}`);
      const local = createHash('sha256').update(await readFile(path.join(this.distDir, name))).digest('hex');
      if (asset.digest !== `sha256:${local}`) throw new Error(`release asset digest mismatch for ${name}`);
    }
  }

  async run() {
    const names = await this.expectedAssets();
    await this.assertAbsent();
    await this.create();
    for (const name of names) await this.upload(name);
    const beforePublish = await this.readOwned(true);
    await this.validateAssets(beforePublish, names);
    const published = await this.request(`${API}/repos/${this.repository}/releases/${this.releaseId}`, {
      method: 'PATCH', expected: [200], label: 'exact-ID publication', body: JSON.stringify({ draft: false }), headers: { 'content-type': 'application/json' },
    });
    this.assertOwned(published.body, { draft: false });
    const readback = await this.readOwned(false);
    await this.validateAssets(readback, names);
    return readback;
  }
}

async function main() {
  const publisher = new OwnedReleasePublisher({
    token: process.env.GH_TOKEN,
    repository: process.env.GH_REPO,
    tag: process.env.GITHUB_REF_NAME,
    distDir: process.env.RELEASE_DIST,
    expectedFile: process.env.RELEASE_EXPECTED_ASSETS,
    prerelease: process.env.RELEASE_PRERELEASE === 'true',
  });
  try {
    const release = await publisher.run();
    console.log(`release_id=${release.id} status=published`);
  } catch (error) {
    console.error(`release_id=${publisher.releaseId ?? 'none'} status=stopped`);
    throw error;
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) await main();

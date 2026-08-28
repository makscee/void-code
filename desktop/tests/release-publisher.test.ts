import { createHash } from 'node:crypto';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import { OwnedReleasePublisher } from '../scripts/release-publisher.mjs';

const json = (status: number, body: unknown, headers: Record<string, string> = {}) => new Response(JSON.stringify(body), { status, headers: { 'content-type': 'application/json', ...headers } });
const empty = (status: number) => new Response('', { status });

function fixture() {
  const root = mkdtempSync(path.join(tmpdir(), 'release-publisher-'));
  writeFileSync(path.join(root, 'a'), 'alpha');
  writeFileSync(path.join(root, 'b'), 'beta');
  writeFileSync(path.join(root, 'expected.txt'), 'a\nb\n');
  return { root, expected: path.join(root, 'expected.txt') };
}

function publisher(fetchImpl: typeof fetch, nonce = '7d94711e-cc56-47e4-a9e8-1c27ad6f72ad') {
  const files = fixture();
  return new OwnedReleasePublisher({ fetchImpl, token: 'secret', repository: 'makscee/void-code', tag: 'v1.2.3', distDir: files.root, expectedFile: files.expected, prerelease: false, nonce });
}

function successApi() {
  const assets: Array<{ name: string; digest: string }> = [];
  let draft = true;
  const marker = 'void-code-release-owner:7d94711e-cc56-47e4-a9e8-1c27ad6f72ad';
  const calls: string[] = [];
  const fetchImpl = async (input: URL | RequestInfo, init: RequestInit = {}) => {
    const url = String(input); const method = init.method ?? 'GET'; calls.push(`${method} ${url}`);
    if (url.endsWith('/releases/tags/v1.2.3')) return empty(404);
    if (url.includes('/releases?per_page=')) return json(200, []);
    if (url.endsWith('/releases') && method === 'POST') return json(201, { id: 42, tag_name: 'v1.2.3', draft: true, body: marker, name: `v1.2.3 ${marker}`, upload_url: 'https://uploads.github.com/repos/makscee/void-code/releases/42/assets{?name,label}', assets: [] });
    if (url.endsWith('/releases/42') && method === 'GET') return json(200, { id: 42, tag_name: 'v1.2.3', draft, body: marker, name: `v1.2.3 ${marker}`, assets });
    if (url.includes('uploads.github.com/repos/makscee/void-code/releases/42/assets?name=')) {
      const name = decodeURIComponent(url.split('name=')[1]);
      const bytes = Buffer.from(await new Response(init.body).arrayBuffer());
      assets.push({ name, digest: `sha256:${createHash('sha256').update(bytes).digest('hex')}` });
      return json(201, { name });
    }
    if (url.endsWith('/releases/42') && method === 'PATCH') { draft = false; return json(200, { id: 42, tag_name: 'v1.2.3', draft: false, body: marker, name: `v1.2.3 ${marker}`, assets }); }
    return json(500, { unexpected: `${method} ${url}` });
  };
  return { fetchImpl: fetchImpl as typeof fetch, calls, assets, setDraft: (value: boolean) => { draft = value; } };
}

describe('owned create-only release publisher', () => {
  it('allows only a verified 404 absence and completes exact-ID publication', async () => {
    const api = successApi();
    await expect(publisher(api.fetchImpl).run()).resolves.toMatchObject({ id: 42, draft: false, tag_name: 'v1.2.3' });
    expect(api.calls.some((call) => call.startsWith('PATCH ') && call.endsWith('/releases/42'))).toBe(true);
    expect(api.calls.some((call) => call.includes('/releases/tags/') && call.startsWith('PATCH '))).toBe(false);
  });

  it.each([401, 403, 429, 500])('fails lookup HTTP %s before create', async (status) => {
    const calls: string[] = [];
    const f = (async (input: URL | RequestInfo) => { calls.push(String(input)); return json(status, {}); }) as typeof fetch;
    await expect(publisher(f).run()).rejects.toThrow(/lookup/i);
    expect(calls.some((call) => call.endsWith('/releases'))).toBe(false);
  });

  it('fails a network lookup before create', async () => {
    await expect(publisher((async () => { throw new Error('network down'); }) as typeof fetch).run()).rejects.toThrow(/network down/);
  });

  it.each([true, false])('refuses a pre-existing draft/public release', async (draft) => {
    const f = (async () => json(200, { id: 9, tag_name: 'v1.2.3', draft })) as typeof fetch;
    await expect(publisher(f).run()).rejects.toThrow(/already exists/i);
  });

  it('refuses a pre-existing draft found by enumeration after tag 404', async () => {
    let n = 0;
    const f = (async () => ++n === 1 ? empty(404) : json(200, [{ id: 9, tag_name: 'v1.2.3', draft: true }])) as typeof fetch;
    await expect(publisher(f).run()).rejects.toThrow(/already exists/i);
  });

  it('treats create 422 race as failure without upload', async () => {
    let n = 0;
    const f = (async () => { n++; return n === 1 ? empty(404) : n === 2 ? json(200, []) : json(422, {}); }) as typeof fetch;
    await expect(publisher(f).run()).rejects.toThrow(/create/i);
  });

  it.each([
    { id: 41, tag_name: 'v1.2.3', draft: true, body: 'void-code-release-owner:7d94711e-cc56-47e4-a9e8-1c27ad6f72ad' },
    { id: 42, tag_name: 'wrong', draft: true, body: 'void-code-release-owner:7d94711e-cc56-47e4-a9e8-1c27ad6f72ad' },
    { id: 42, tag_name: 'v1.2.3', draft: false, body: 'void-code-release-owner:7d94711e-cc56-47e4-a9e8-1c27ad6f72ad' },
    { id: 42, tag_name: 'v1.2.3', draft: true, body: 'other' },
  ])('fails wrong created identity before upload: %j', async (created) => {
    let n = 0;
    const f = (async () => ++n === 1 ? empty(404) : n === 2 ? json(200, []) : json(201, created)) as typeof fetch;
    await expect(publisher(f).run()).rejects.toThrow(/ownership|identity/i);
  });

  it('stops before the next mutation if another run publishes its draft', async () => {
    const api = successApi();
    let reads = 0;
    const wrapped = (async (input: URL | RequestInfo, init: RequestInit = {}) => {
      if (String(input).endsWith('/releases/42') && (init.method ?? 'GET') === 'GET' && ++reads === 2) api.setDraft(false);
      return api.fetchImpl(input, init);
    }) as typeof fetch;
    await expect(publisher(wrapped).run()).rejects.toThrow(/draft|ownership/i);
    expect(api.assets).toHaveLength(1);
  });

  it('fails a wrong remote digest before publish', async () => {
    const api = successApi();
    const wrapped = (async (input: URL | RequestInfo, init: RequestInit = {}) => {
      const response = await api.fetchImpl(input, init);
      if (String(input).endsWith('/releases/42') && (init.method ?? 'GET') === 'GET' && api.assets.length === 2) {
        const body = await response.json() as Record<string, unknown>;
        return json(200, { ...body, assets: [{ ...api.assets[0], digest: `sha256:${'0'.repeat(64)}` }, api.assets[1]] });
      }
      return response;
    }) as typeof fetch;
    await expect(publisher(wrapped).run()).rejects.toThrow(/digest/i);
    expect(api.calls.some((call) => call.startsWith('PATCH '))).toBe(false);
  });

  it('fails a missing remote asset before publish', async () => {
    const api = successApi();
    const wrapped = (async (input: URL | RequestInfo, init: RequestInit = {}) => {
      const response = await api.fetchImpl(input, init);
      if (String(input).endsWith('/releases/42') && (init.method ?? 'GET') === 'GET' && api.assets.length === 2) {
        const body = await response.json() as Record<string, unknown>;
        return json(200, { ...body, assets: api.assets.slice(0, 1) });
      }
      return response;
    }) as typeof fetch;
    await expect(publisher(wrapped).run()).rejects.toThrow(/asset/i);
    expect(api.calls.some((call) => call.startsWith('PATCH '))).toBe(false);
  });
});

import { describe, expect, it, vi } from 'vitest';
import { installUpdaterRequestBoundary } from '../src/main/electron-updater-adapter';
import { UPDATE_MANIFEST_MAX_BYTES, UpdaterRequestAuthorizer } from '../src/main/stable-update';

const size = 42;
const artifactUrl = 'https://vc.makscee.ru/download/windows/Void-Code-0.1.3-beta.2-windows-x64.exe';
const immutableUrl = 'https://github.com/makscee/void-code/releases/download/desktop-v0.1.3-beta.2/Void-Code-0.1.3-beta.2-windows-x64.exe';
const releaseUrl = 'https://release-assets.githubusercontent.com/github-production-release-asset/123/abc?sig=x';

function fakeBoundary() {
  let beforeRequest!: (details: { url: string; webContentsId: number }, callback: (reply: { cancel: boolean }) => void) => void;
  let beforeRedirect!: (details: { url: string; redirectURL: string; webContentsId: number }) => void;
  let headersReceived!: (details: { url: string; statusCode: number; responseHeaders?: Record<string, string[]>; webContentsId: number }, callback: (reply: { cancel: boolean }) => void) => void;
  const session = { webRequest: {
    onBeforeRequest: vi.fn((_filter, listener) => { beforeRequest = listener; }),
    onBeforeRedirect: vi.fn((_filter, listener) => { beforeRedirect = listener; }),
    onHeadersReceived: vi.fn((_filter, listener) => { headersReceived = listener; }),
  } };
  const authorizer = new UpdaterRequestAuthorizer(); installUpdaterRequestBoundary(session as never, authorizer);
  const authorize = () => authorizer.authorize({ artifactUrl, immutableUrl, size }); authorize();
  return {
    authorize,
    request(url: string) { let cancel = false; beforeRequest({ url, webContentsId: -1 }, (reply) => { cancel = reply.cancel; }); return !cancel; },
    redirect(url: string, redirectURL: string) { beforeRedirect({ url, redirectURL, webContentsId: -1 }); },
    response(url: string, statusCode: number, responseHeaders?: Record<string, string[]>) { let cancel = false; headersReceived({ url, statusCode, responseHeaders, webContentsId: -1 }, (reply) => { cancel = reply.cancel; }); return !cancel; },
  };
}

function metadataUrl(): string { return `https://vc.makscee.ru/download/windows/latest.yml?noCache=${Date.now().toString(32)}`; }

describe('updater response header artifact boundary', () => {
  it('body-bounds the one authorized metadata request and keeps approved artifact redirects working', () => {
    const boundary = fakeBoundary(); const metadata = metadataUrl();
    expect(boundary.request(metadata)).toBe(true);
    expect(boundary.response(metadata, 200, { 'Content-Length': ['42'] })).toBe(true);
    expect(boundary.request(artifactUrl)).toBe(true);
    expect(boundary.response(artifactUrl, 302, { location: [immutableUrl], 'Content-Length': ['0'] })).toBe(true);
    boundary.redirect(artifactUrl, immutableUrl); expect(boundary.request(immutableUrl)).toBe(true);
    expect(boundary.response(immutableUrl, 302, { location: [releaseUrl], 'Content-Length': ['0'] })).toBe(true);
    boundary.redirect(immutableUrl, releaseUrl); expect(boundary.request(releaseUrl)).toBe(true);
    boundary.redirect(immutableUrl, 'https://attacker.example/artifact.exe');
    expect(boundary.request('https://attacker.example/artifact.exe')).toBe(false);
  });

  it.each([301, 302, 303, 307, 308])('allows approved artifact redirect status %i with one HTTPS location and zero body', (status) => {
    const boundary = fakeBoundary(); expect(boundary.request(artifactUrl)).toBe(true);
    expect(boundary.response(artifactUrl, status, { Location: [immutableUrl], 'Content-Length': ['0'] })).toBe(true);
  });

  it.each([300, 304, 305, 306, 400, 500])('rejects non-approved artifact response status %i', (status) => {
    const boundary = fakeBoundary(); expect(boundary.request(artifactUrl)).toBe(true);
    expect(boundary.response(artifactUrl, status, { Location: [immutableUrl], 'Content-Length': ['0'] })).toBe(false);
  });

  it.each([
    ['missing body length', { Location: [immutableUrl] }],
    ['chunked body', { Location: [immutableUrl], 'Content-Length': ['0'], 'Transfer-Encoding': ['chunked'] }],
    ['compressed body', { Location: [immutableUrl], 'Content-Length': ['0'], 'Content-Encoding': ['gzip'] }],
    ['nonzero body', { Location: [immutableUrl], 'Content-Length': ['1'] }],
    ['missing location', { 'Content-Length': ['0'] }],
    ['duplicate location values', { Location: [immutableUrl, immutableUrl], 'Content-Length': ['0'] }],
    ['duplicate location names', { Location: [immutableUrl], location: [immutableUrl], 'Content-Length': ['0'] }],
    ['non-HTTPS location', { Location: ['http://vc.makscee.ru/artifact.exe'], 'Content-Length': ['0'] }],
    ['malformed location', { Location: ['not a url'], 'Content-Length': ['0'] }],
  ])('rejects an artifact redirect with %s', (_name, headers) => {
    const boundary = fakeBoundary(); expect(boundary.request(artifactUrl)).toBe(true);
    expect(boundary.response(artifactUrl, 302, headers)).toBe(false);
  });

  it.each([
    ['non-2xx', 500, { 'Content-Length': ['42'] }],
    ['redirect', 302, { Location: ['https://vc.makscee.ru/download/windows/latest.yml'], 'Content-Length': ['0'] }],
    ['missing length', 200, undefined],
    ['zero length', 200, { 'Content-Length': ['0'] }],
    ['non-canonical length', 200, { 'Content-Length': ['042'] }],
    ['oversized', 200, { 'Content-Length': [String(UPDATE_MANIFEST_MAX_BYTES + 1)] }],
    ['duplicate values', 200, { 'Content-Length': ['42', '42'] }],
    ['duplicate names', 200, { 'Content-Length': ['42'], 'content-length': ['42'] }],
    ['chunked', 200, { 'Content-Length': ['42'], 'Transfer-Encoding': ['chunked'] }],
    ['compressed', 200, { 'Content-Length': ['42'], 'Content-Encoding': ['gzip'] }],
  ])('rejects a metadata response with %s', (_name, status, headers) => {
    const boundary = fakeBoundary(); const metadata = metadataUrl(); expect(boundary.request(metadata)).toBe(true);
    expect(boundary.response(metadata, status, headers)).toBe(false);
  });

  it('consumes each metadata and initial artifact request/response decision until re-authorized', () => {
    const boundary = fakeBoundary(); const metadata = metadataUrl();
    expect(boundary.request(metadata)).toBe(true); expect(boundary.request(metadata)).toBe(false);
    expect(boundary.response(metadata, 200, { 'Content-Length': ['42'] })).toBe(true);
    expect(boundary.response(metadata, 200, { 'Content-Length': ['42'] })).toBe(false);
    expect(boundary.request(artifactUrl)).toBe(true); expect(boundary.request(artifactUrl)).toBe(false);
    expect(boundary.response(artifactUrl, 200, { 'Content-Length': ['42'] })).toBe(true);
    expect(boundary.response(artifactUrl, 200, { 'Content-Length': ['42'] })).toBe(false);
    boundary.authorize();
    expect(boundary.request(metadataUrl())).toBe(true); expect(boundary.request(artifactUrl)).toBe(true);
  });

  it.each([
    ['missing length', undefined],
    ['chunked', { 'Transfer-Encoding': ['chunked'] }],
    ['compressed', { 'Content-Length': ['42'], 'Content-Encoding': ['gzip'] }],
    ['too large', { 'Content-Length': ['43'] }],
    ['too small', { 'content-length': ['41'] }],
    ['duplicate values', { 'Content-Length': ['42', '42'] }],
    ['duplicate names', { 'Content-Length': ['42'], 'content-length': ['42'] }],
    ['comma joined', { 'Content-Length': ['42, 42'] }],
  ])('rejects a final artifact response with %s', (_name, headers) => {
    const boundary = fakeBoundary(); expect(boundary.request(artifactUrl)).toBe(true);
    expect(boundary.response(artifactUrl, 200, headers)).toBe(false);
  });

  it('allows a final artifact response only at the exact signed content length', () => {
    const boundary = fakeBoundary(); expect(boundary.request(artifactUrl)).toBe(true);
    expect(boundary.response(artifactUrl, 200, { 'Content-Length': [String(size)] })).toBe(true);
  });
});

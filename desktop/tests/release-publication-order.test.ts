import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { asList, asMap, asText, parseWorkflow } from './workflow-yaml';

const text = readFileSync(new URL('../../.github/workflows/release.yml', import.meta.url), 'utf8');
const jobs = asMap(parseWorkflow(text).jobs);
const release = asMap(jobs.release);
const publishAuth = asMap(jobs['publish-auth']);
const publisherSource = readFileSync(new URL('../scripts/release-publisher.mjs', import.meta.url), 'utf8');
const steps = asList(release.steps).map(asMap);
const transcript = steps.map((step) => `${asText(step.name)}\n${asText(step.uses)}\n${asText(step.run)}\n${JSON.stringify(asMap(step.with))}`).join('\n');
const needs = asList(release.needs).map(asText);

/** Product contract: no public release or feed update can precede every enabled
 * package, exact-manifest verification, and provenance. */
describe('release publication is terminal and manifest-verified', () => {
  it('serializes ownership per repository and exact ref without cancellation', () => {
    const concurrency = asMap(parseWorkflow(text).concurrency);
    expect(asText(concurrency.group)).toBe('release-${{ github.repository }}-${{ github.ref }}');
    expect(asText(concurrency['cancel-in-progress'])).toBe('false');
  });

  it('waits for CLI and both conditionally enabled desktop producers', () => {
    expect(needs.slice().sort()).toEqual(['build', 'desktop-mac-app', 'desktop-windows-app']);
    const condition = asText(release.if);
    expect(condition).toContain("vars.DESKTOP_RELEASE != 'true'");
    expect(condition).toContain("needs.desktop-mac-app.result == 'success'");
    expect(condition).toContain("needs.desktop-windows-app.result == 'success'");
    expect(condition).toContain("needs.build.result == 'success'");
  });

  it('keeps the desktop-disabled path live despite skipped desktop needs', () => {
    expect(asText(release.if)).toContain('!cancelled()');
    expect(asText(release.if)).toContain("vars.DESKTOP_RELEASE != 'true'");
  });

  it('uses one create-only, exact-ID publisher and never a later desktop attacher', () => {
    expect(asText(jobs['desktop-attach'])).toBe('');
    expect(transcript).toContain('node desktop/scripts/release-publisher.mjs');
    expect(publisherSource).toContain("method: 'POST', expected: [201], label: 'draft create'");
    expect(publisherSource).toContain('releases/${this.releaseId}');
    expect(publisherSource).not.toContain('softprops/action-gh-release');
  });

  it('verifies exact 8/11 asset manifests, checksums, digests, and provenance before publish', () => {
    expect(transcript).toContain('EXPECTED_CLI_COUNT=8');
    expect(transcript).toContain('EXPECTED_DESKTOP_COUNT=11');
    expect(transcript).toContain('sha256sum -c SHA256SUMS');
    expect(transcript).toContain('attest-build-provenance');
    expect(publisherSource).toContain('asset?.digest');
    expect(transcript).toContain('comm -3');
    expect(publisherSource.indexOf('await this.create()')).toBeLessThan(publisherSource.indexOf("method: 'PATCH'"));
  });

  it('allows auth publication only after the terminal publisher succeeds', () => {
    expect(asList(publishAuth.needs).map(asText)).toEqual(['release']);
    expect(publisherSource).toContain("body: JSON.stringify({ draft: false })");
  });

  it('names every required desktop asset, so deleting one is red', () => {
    expect(transcript).toContain('void-code-mac-arm64.zip');
    expect(transcript).toContain('void-code-mac-x64.zip');
    expect(transcript).toContain('Void-Code-0.1.0-windows-x64.exe');
  });
});

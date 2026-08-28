import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { asList, asMap, asText, parseWorkflow } from './workflow-yaml';

const releaseText = readFileSync(new URL('../../.github/workflows/release.yml', import.meta.url), 'utf8');
const macText = readFileSync(new URL('../../.github/workflows/desktop-mac-app.yml', import.meta.url), 'utf8');
const windowsText = readFileSync(new URL('../../.github/workflows/desktop-windows-app.yml', import.meta.url), 'utf8');
const jobs = asMap(parseWorkflow(releaseText).jobs);
const publisher = asMap(jobs.release);
const steps = asList(publisher.steps).map(asMap);
const transcript = steps.map((step) => `${asText(step.uses)}\n${asText(step.run)}\n${JSON.stringify(asMap(step.with))}`).join('\n');

describe('one publisher collects every qualified artifact before publication', () => {
  it('downloads all run artifacts merged into one staging directory', () => {
    const downloads = steps.filter((step) => /actions\/download-artifact/.test(asText(step.uses)));
    expect(downloads).toHaveLength(1);
    const settings = asMap(downloads[0].with);
    expect(asText(settings.path)).toBe('dist');
    expect(asText(settings['merge-multiple'])).toBe('true');
    expect(asText(settings.name)).toBe('');
    expect(asText(settings.pattern)).toBe('');
  });

  it('derives and verifies an exact manifest before draft upload', () => {
    expect(transcript).toContain('expected-assets.txt');
    expect(transcript).toContain('actual-assets.txt');
    expect(transcript).toContain('comm -3');
    expect(transcript).toContain('test ! -s manifest-difference.txt');
    expect(transcript.indexOf('test ! -s manifest-difference.txt')).toBeLessThan(transcript.indexOf('"draft":"true"'));
  });

  it('accounts for both mac rows and the Windows installer', () => {
    expect(macText).toContain('void-code-mac-${{ matrix.arch }}.zip');
    expect(windowsText).toContain('Void-Code-*-windows-x64.exe');
    for (const name of ['void-code-mac-arm64.zip', 'void-code-mac-x64.zip', 'Void-Code-0.1.0-windows-x64.exe']) {
      expect(transcript).toContain(name);
    }
  });

  it('has no post-publication attachment job or upload', () => {
    expect(jobs['desktop-attach']).toBeUndefined();
    expect(releaseText).not.toMatch(/gh\s+release\s+upload/);
    expect(transcript.match(/softprops\/action-gh-release/g)).toHaveLength(1);
  });
});

import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { asList, asMap, asText, parseWorkflow } from './workflow-yaml';

const text = readFileSync(new URL('../../.github/workflows/release.yml', import.meta.url), 'utf8');
const workflow = parseWorkflow(text);
const jobs = asMap(workflow.jobs);
const release = asMap(jobs.release);
const releaseSteps = asList(release.steps).map(asMap);
const transcript = releaseSteps.map((step) => `${asText(step.run)}\n${JSON.stringify(asMap(step.with))}`).join('\n');

describe('desktop release opt-in has two live, fail-closed paths', () => {
  it('keeps desktop producers strictly behind DESKTOP_RELEASE=true', () => {
    expect(asText(asMap(jobs['desktop-mac-app']).if)).toBe("${{ vars.DESKTOP_RELEASE == 'true' }}");
    expect(asText(asMap(jobs['desktop-windows-app']).if)).toBe("${{ vars.DESKTOP_RELEASE == 'true' }}");
  });

  it('makes the publisher wait on both skipped-or-success desktop jobs', () => {
    expect(asList(release.needs).map(asText).sort()).toEqual(['build', 'desktop-mac-app', 'desktop-windows-app']);
    const condition = asText(release.if);
    expect(condition).toContain('!cancelled()');
    expect(condition).toContain("vars.DESKTOP_RELEASE != 'true'");
    expect(condition).toContain("needs.desktop-mac-app.result == 'success'");
    expect(condition).toContain("needs.desktop-windows-app.result == 'success'");
  });

  it('defines exact CLI-only and desktop-enabled counts without publishing desktop to auth', () => {
    expect(transcript).toContain('EXPECTED_CLI_COUNT=8');
    expect(transcript).toContain('EXPECTED_DESKTOP_COUNT=11');
    const auth = JSON.stringify(jobs['publish-auth']);
    expect(auth).toContain('"needs":"release"');
    expect(auth).not.toMatch(/void-code-mac|Void-Code-/);
  });

  it('remains tag-push only and creates no tag', () => {
    const on = asMap(workflow.on);
    expect(Object.keys(on)).toEqual(['push']);
    expect(asList(asMap(on.push).tags)).toEqual(['v*.*.*', '!v*.*.*-vi20.*']);
    expect(text).not.toMatch(/\bgit\s+tag\b|\bgit\s+push\b[^\n]*(?:--tags|--follow-tags|\stag)/);
  });
});

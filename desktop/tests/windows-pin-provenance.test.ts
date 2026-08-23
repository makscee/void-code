import { execFileSync } from 'node:child_process';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import pins from '../scripts/resource-pins.json';

const repo = path.resolve('..');
const git = (...args: string[]) => execFileSync('git', args, { cwd: repo, encoding: 'utf8' }).trim();

/**
 * The Windows pin names bytes that ship to users, so it has to say where those
 * bytes came from. Three identities are involved and conflating them is what
 * made the previous pin unverifiable: the release, the commit the release was
 * built from, and the last commit that touched the CLI inside it.
 *
 * Freshness — whether the pinned release contains current work — is deliberately
 * not asserted here. It is a release-readiness question, and pinning it to a
 * moving `main` made this suite red between every release.
 */
describe('windows vc pin provenance', () => {
  const vc = pins.windows.vc as Record<string, string>;

  it('names a published release asset rather than an anonymous local build', () => {
    expect(vc.provenance).toBe('github-release');
    expect(vc.repository).toBe('makscee/void-code');
    expect(vc.releaseTag).toMatch(/^v\d+\.\d+\.\d+/);
    expect(vc.assetName).toBe('vc-windows-amd64.exe');
    expect(vc.sha256).toMatch(/^[0-9a-f]{64}$/);
  });

  it('pins the commit the release was actually built from', () => {
    expect(vc.releaseCommit).toBe(git('rev-list', '-n1', vc.releaseTag));
  });

  it('pins the CLI revision contained in that release, not the one on main', () => {
    expect(vc.cliSourceCommit).toBe(git('log', '-1', '--format=%H', vc.releaseTag, '--', 'cmd/vc'));
  });

  it('keeps the pinned CLI revision inside the pinned release', () => {
    expect(() => git('merge-base', '--is-ancestor', vc.cliSourceCommit, vc.releaseTag)).not.toThrow();
  });
});

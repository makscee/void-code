import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { buildSupportReport, copySupportReport, saveSupportReport, serializeSupportReport } from '../src/main/support-report';
import { supportReportValid } from '../scripts/windows-pilot-rehearsal-lib.mjs';

const base = {
  appVersion: '0.1.0', platform: 'win32', architecture: 'x64', generatedAt: '2026-07-27T01:02:03.000Z',
  workspace: 'missing' as const, runtime: 'start_failed' as const, recoveryCode: 'SESSION_START_FAILED' as const,
};

describe('allowlisted support report', () => {
  it('serializes only deterministic coarse support fields', () => {
    expect(JSON.parse(serializeSupportReport(buildSupportReport(base)))).toEqual({
      schema: 1,
      app: { name: 'Void Code', version: '0.1.0' },
      system: { platform: 'windows', architecture: 'x64' },
      generatedAt: '2026-07-27T01:02:03.000Z',
      state: { workspace: 'missing', runtime: 'start_failed', recoveryCode: 'SESSION_START_FAILED' },
    });
  });

  it('cannot collect adversarial extra fields or forbidden strings', () => {
    const poisoned = {
      ...base,
      token: 'secret-token', accessCode: 'ABCD-EFGH', env: { HOME: '/Users/accountant' },
      workspacePath: 'C:\\Clients\\Sensitive Client', terminalOutput: 'private prompt', stack: 'at /Users/accountant/app',
      commandLine: 'pi --api-key secret', fileContents: 'bank details', sessionId: 'private-session', username: 'accountant',
    };
    const text = serializeSupportReport(buildSupportReport(poisoned));
    for (const forbidden of ['secret-token', 'ABCD-EFGH', 'HOME', '/Users/', 'Sensitive Client', 'private prompt', 'bank details', 'private-session', 'accountant', 'api-key']) expect(text).not.toContain(forbidden);
    expect(Object.keys(JSON.parse(text))).toEqual(['schema', 'app', 'system', 'generatedAt', 'state']);
  });

  it('copies or saves exactly the allowlisted report and keeps cancellation inert', async () => {
    const report = buildSupportReport(base); let copied = '';
    expect(copySupportReport(report, (text) => { copied = text; })).toEqual({ action: 'copied' });
    expect(copied).toBe(serializeSupportReport(report));

    const writes: Array<{ file: string; text: string }> = [];
    await expect(saveSupportReport(report, async () => '/chosen/support.json', (file, text) => writes.push({ file, text }))).resolves.toEqual({ action: 'saved' });
    expect(writes).toEqual([{ file: '/chosen/support.json', text: serializeSupportReport(report) }]);
    await expect(saveSupportReport(report, async () => null, (file, text) => writes.push({ file, text }))).resolves.toEqual({ action: 'cancelled' });
    expect(writes).toHaveLength(1);
  });

  it('keeps copy/save authority in main without broad collection or returning a path', () => {
    const main = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');
    const preload = readFileSync(new URL('../src/preload/index.ts', import.meta.url), 'utf8');
    expect(main).toContain('copySupportReport(supportReport(raw)');
    expect(main).toContain('dialog.showSaveDialog');
    expect(main).not.toContain("action: 'saved', filePath");
    expect(preload).toContain('support: Object.freeze');
    expect(preload).not.toContain('readFile');
  });
});

// ---------------------------------------------------------------------------
// The version check: widened, on purpose, and with the reason written down.
//
// support-report.ts refused anything but ^\d+\.\d+\.\d+$. That was correct
// while the only version in the system was the literal 0.1.0, and it becomes
// wrong the moment the app is stamped from `git describe`: a build off the tag
// reports 0.2.50-3-gabc1234, which is a real version of a real build, and the
// old rule throws on it. A support report that cannot be generated for a branch
// build is a support report that fails exactly when support is needed.
//
// THE CHOICE. Two ways out, and they are not equivalent:
//
//   normalize, then report -- round 0.2.50-3-gabc1234 down to 0.2.50 before
//     writing it. This throws away the one thing the field is for. The reason a
//     support report carries a version is to identify the build in front of the
//     person; a report that says "0.2.50" about something that is three commits
//     past 0.2.50 sends the reader to the wrong source tree. Rejected.
//
//   widen the rule to the grammar the build actually produces -- accept
//     MAJOR.MINOR.PATCH with an optional prerelease suffix, which is the whole
//     range of what scripts/build-version.mjs can emit, and nothing else.
//     Chosen.
//
// WHAT THE RULE WAS FOR IS KEPT. It was never a claim that only three-number
// releases exist; it was a refusal to let junk into a document a person reads
// and quotes. Everything that would show up if the stamp went wrong is still
// refused below -- `dev`, `vc dev`, the leading `v` of the git spelling, an
// empty string -- so widening costs none of the original force.
// ---------------------------------------------------------------------------

const ACCEPTED = ['0.1.0', '0.2.50', '0.2.50-3-gabc1234', '1.0.0-rc.1', '0.0.0-gabc1234'] as const;
const REFUSED = ['dev', 'vc dev', 'v0.2.50', '', '   ', '0.2', '0.2.50 dirty', 'latest', '0.2.50\n'] as const;

describe('the support report states the version of the build it was taken from', () => {
  it.each(ACCEPTED)('accepts %s, because that is a version the build can produce', (appVersion) => {
    expect(buildSupportReport({ ...base, appVersion }).app.version).toBe(appVersion);
  });

  it.each(REFUSED)('refuses %j rather than putting it in front of a support person', (appVersion) => {
    expect(() => buildSupportReport({ ...base, appVersion })).toThrow('invalid app version');
  });

  it('keeps the branch build distinguishable from the release it came after', () => {
    // The whole reason normalization was rejected: these two must not produce
    // the same report.
    expect(buildSupportReport({ ...base, appVersion: '0.2.50-3-gabc1234' }).app.version)
      .not.toBe(buildSupportReport({ ...base, appVersion: '0.2.50' }).app.version);
  });

  it('is the same rule the Windows rehearsal validator applies', () => {
    // Two files hold this rule -- src/main/support-report.ts writes reports and
    // scripts/windows-pilot-rehearsal-lib.mjs validates them during the pilot
    // rehearsal -- and a report the writer emits that the validator rejects is
    // a rehearsal that fails on a healthy build. They are checked against one
    // list rather than trusted to have been edited together.
    const report = (version: string) => ({ schema: 1, app: { name: 'Void Code', version }, system: { platform: 'windows', architecture: 'x64' }, generatedAt: '2026-07-27T01:02:03.000Z', state: { workspace: 'ready', runtime: 'running', recoveryCode: 'NONE' } });
    for (const version of ACCEPTED) expect(supportReportValid(report(version)), `the rehearsal validator rejects ${version}`).toBe(true);
    for (const version of REFUSED) expect(supportReportValid(report(version)), `the rehearsal validator accepts ${JSON.stringify(version)}`).toBe(false);
  });

  it('spells the rule identically in both files, since neither can import the other', () => {
    // Not a check of any particular spelling -- the two are compared against
    // EACH OTHER, so the implementer picks the wording. What is refused is the
    // two drifting: one is TypeScript compiled into the app, the other a plain
    // script used by the rehearsal, so nothing but a reader keeps them level,
    // and the behaviour tables above only notice a drift for the versions they
    // happen to list.
    const literal = (relative: string) => {
      const source = readFileSync(new URL(relative, import.meta.url), 'utf8');
      const found = /\/\^\\d\+[^/\n]*\$\//.exec(source)?.[0];
      expect(found, `${relative} contains no app-version regular expression`).toBeDefined();
      return found;
    };
    expect(literal('../scripts/windows-pilot-rehearsal-lib.mjs')).toBe(literal('../src/main/support-report.ts'));
  });
});

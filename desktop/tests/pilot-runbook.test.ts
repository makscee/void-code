import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const runbook = readFileSync(new URL('../docs/windows-accountant-pilot-runbook.md', import.meta.url), 'utf8');

describe('signed 0.1.2 Russian-default one-click acceptance runbook', () => {
  it('requires exact signed frozen identities and rejects unsigned release acceptance', () => {
    expect(runbook).toContain('Stable-track reference only');
    expect(runbook).toContain('current source package is `0.1.3-beta.5`');
    for (const phrase of ['Void-Code-0.1.2-windows-x64.exe', 'older separately signed build', 'Get-FileHash', 'Get-AuthenticodeSignature', 'AuthentiCode-signed'.replace('AuthentiCode', 'Authenticode'), 'exact approved publisher', 'blocked candidate', 'Signing authority']) expect(runbook).toContain(phrase);
    expect(runbook).not.toContain('SMARTSCREEN_UNSIGNED PASS');
    expect(runbook).not.toContain('MOTW_PRESENT PASS');
    expect(runbook).toContain('Do not accept SmartScreen bypass');
    expect(runbook).toContain('0.1.1 is `RETIRED_INTERNAL_REVIEW_FAILED`');
    expect(runbook).toContain('Only 0.1.2 may become the post-fix candidate');
  });

  it('covers Russian clean launch and explicit English/Russian restart persistence', () => {
    for (const phrase of ['CLEAN_RUSSIAN PASS', 'English', 'ENGLISH_RESTART PASS', 'Russian', 'RUSSIAN_RESTART PASS', 'NO_LOCALE_FETCH PASS', 'no locale network fetch']) expect(runbook).toContain(phrase);
  });

  it('covers real one-click progress, verification, automatic restart, and preserved state', () => {
    for (const phrase of ['Update now', 'downloading with finite progress', 'verifying', 'installing/restarting', 'restarts automatically', 'ONE_CLICK_PROGRESS_VERIFY_RESTART PASS', 'LOCALE_UPDATE_PRESERVED PASS', 'AUTH_APP_STATE_UPDATE_PRESERVED PASS']) expect(runbook).toContain(phrase);
    for (const forbidden of ['No browser', 'Downloads folder', 'external GitHub page', 'manually launched installer']) expect(runbook).toContain(forbidden);
  });

  it('requires reboot persistence, no locale fetch, and no survivors', () => {
    for (const phrase of ['WINDOWS_REBOOT_LOCALE PASS', 'WINDOWS_REBOOT_STATE PASS', 'NO_UPDATER_INSTALLER_TEMP_SURVIVORS PASS', 'NO_PROCESS_SURVIVORS PASS', 'partial installer', 'pending download']) expect(runbook).toContain(phrase);
  });

  it('states that the procedure is not an E2E claim and keeps publication closed', () => {
    expect(runbook).toContain('not a claim that acceptance ran');
    expect(runbook).toContain('not installed E2E evidence');
    expect(runbook).toContain('production publication/portal mutation remains closed');
  });
});

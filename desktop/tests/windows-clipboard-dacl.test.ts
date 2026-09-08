import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, mkdtempSync, readFileSync, realpathSync, rmSync, statSync, symlinkSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { describe, expect, it } from 'vitest';
import * as clipboardPaste from '../src/main/clipboard-paste';

const { createClipboardImageStorage } = clipboardPaste;

type ClipboardStorageNamespaceSeam = typeof clipboardPaste & {
  clipboardStorageRoot?: (temporaryDirectory: string, userData: string) => string;
};
import { asList, asMap, asText, parseWorkflow } from './workflow-yaml';

const DACL_TEST = 'tests/windows-clipboard-dacl.test.ts';
const DACL_TEST_COMMAND = `npm test -- --run ${DACL_TEST}`;
const BROAD_SIDS = new Set(['S-1-1-0', 'S-1-5-11', 'S-1-5-32-545']);
const SYSTEM_SID = 'S-1-5-18';
const ADMINISTRATORS_SID = 'S-1-5-32-544';
const TEST_UUID = '00000000-0000-4000-8000-00000000daac';

type Ace = {
  sid: string;
  type: string;
};

type AclEntry = {
  path: string;
  protected: boolean;
  aces: Ace[];
};

type AclReport = {
  currentUserSid: string;
  entries: AclEntry[];
};

function systemRoot(): string {
  const entry = Object.entries(process.env).find(([name]) => name.toLowerCase() === 'systemroot');
  if (entry?.[1] === undefined || entry[1] === '') throw new Error('Windows test runner has no SystemRoot');
  return entry[1];
}

function inspectDacls(targets: string[]): AclReport {
  const powershell = path.join(systemRoot(), 'System32', 'WindowsPowerShell', 'v1.0', 'powershell.exe');
  expect(path.isAbsolute(powershell), 'the DACL query must not resolve PowerShell through PATH').toBe(true);
  expect(existsSync(powershell), `absolute Windows PowerShell is absent: ${powershell}`).toBe(true);

  const script = String.raw`
$ErrorActionPreference = 'Stop'
[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false)
$entries = foreach ($target in $args) {
  $acl = Get-Acl -LiteralPath $target
  $aces = @($acl.Access | ForEach-Object {
    [pscustomobject]@{
      sid = $_.IdentityReference.Translate([System.Security.Principal.SecurityIdentifier]).Value
      type = $_.AccessControlType.ToString()
    }
  })
  [pscustomobject]@{ path = $target; protected = $acl.AreAccessRulesProtected; aces = $aces }
}
[pscustomobject]@{
  currentUserSid = [System.Security.Principal.WindowsIdentity]::GetCurrent().User.Value
  entries = @($entries)
} | ConvertTo-Json -Compress -Depth 5
`;

  const json = execFileSync(powershell, [
    '-NoLogo',
    '-NoProfile',
    '-NonInteractive',
    '-Command',
    script,
    ...targets,
  ], { encoding: 'utf8', windowsHide: true });
  return JSON.parse(json) as AclReport;
}

const windowsIt = process.platform === 'win32' ? it : it.skip;

describe('Windows clipboard image storage uses the per-user temp DACL', () => {
  windowsIt('keeps the scoped root, directory, and PNG inside the inherited per-user temp DACL, then removes them', () => {
    const temporaryDirectory = path.resolve(os.tmpdir());
    expect(path.isAbsolute(temporaryDirectory), 'the real per-user temp directory must resolve to an absolute path').toBe(true);
    const storageRoot = (clipboardPaste as ClipboardStorageNamespaceSeam).clipboardStorageRoot;
    expect(storageRoot, 'clipboard storage must have a userData namespace seam').toBeTypeOf('function');
    if (!storageRoot) return;

    const userData = path.join(temporaryDirectory, 'void-code-dacl-test-user-data');
    mkdirSync(userData, { recursive: true });
    const root = storageRoot(os.tmpdir(), userData);
    const expectedDirectory = path.join(root, `void-code-clipboard-${process.pid}-${TEST_UUID}`);
    rmSync(root, { recursive: true, force: true });
    let cleanup: (() => void) | undefined;

    try {
      const store = createClipboardImageStorage({
        temporaryDirectory: () => root,
        uniqueId: () => TEST_UUID,
        processId: process.pid,
        now: () => Date.UTC(2026, 8, 8),
      });
      cleanup = store.cleanup;
      const png = Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
      const image = store.writeImage(png);

      expect(root).toBe(path.join(temporaryDirectory, path.basename(root)));
      expect(store.directory).toBe(expectedDirectory);
      expect(path.dirname(store.directory)).toBe(root);
      expect(statSync(root).isDirectory()).toBe(true);
      expect(statSync(store.directory).isDirectory()).toBe(true);
      expect(path.dirname(image)).toBe(store.directory);
      expect(path.extname(image)).toBe('.png');
      expect(readFileSync(image)).toEqual(png);

      const report = inspectDacls([root, store.directory, image]);
      expect(report.entries.map((entry) => entry.path)).toEqual([root, store.directory, image]);
      expect(report.currentUserSid).toMatch(/^S-1-/);
      const insideTrustBoundary = new Set([report.currentUserSid, SYSTEM_SID, ADMINISTRATORS_SID]);

      for (const entry of report.entries) {
        expect(entry.protected, `${entry.path} disables inheritance from the per-user temp DACL`).toBe(false);
        const allowSids = entry.aces.filter((ace) => ace.type === 'Allow').map((ace) => ace.sid);
        expect(allowSids, `${entry.path} has no Allow ACE for the current user`).toContain(report.currentUserSid);
        expect(allowSids.filter((sid) => BROAD_SIDS.has(sid)), `${entry.path} grants a broad Windows principal`).toEqual([]);
        expect(allowSids.filter((sid) => !insideTrustBoundary.has(sid)), `${entry.path} grants outside the current-user/SYSTEM/administrators trust boundary`).toEqual([]);
      }

      store.cleanup();
      cleanup = undefined;
      expect(existsSync(store.directory), 'normal cleanup retained the clipboard directory').toBe(false);
    } finally {
      cleanup?.();
      rmSync(root, { recursive: true, force: true });
      rmSync(userData, { recursive: true, force: true });
    }
  });
});

// Windows accepts both spellings of a directory and junction aliases, but path.resolve preserves
// their text. This runs the production canonicalizer against the NTFS identities that a packaged
// desktop actually receives; non-Windows hosts cannot make that claim and skip it.
describe('Windows clipboard storage namespace uses native userData identity', () => {
  windowsIt('maps a case variant and a real junction to the physical userData namespace only', () => {
    const temporaryDirectory = path.resolve(os.tmpdir());
    const sandbox = mkdtempSync(path.join(temporaryDirectory, 'void-code-clipboard-canonical-userdata-'));
    const physicalUserData = path.join(sandbox, 'UserData-Primary');
    const caseVariant = path.join(sandbox, 'uSERdATA-pRIMARY');
    const junctionAlias = path.join(sandbox, 'UserData-Junction');
    const differentPhysicalUserData = path.join(sandbox, 'UserData-Second');

    try {
      mkdirSync(physicalUserData);
      mkdirSync(differentPhysicalUserData);
      symlinkSync(physicalUserData, junctionAlias, 'junction');

      expect(existsSync(caseVariant), 'windows-latest must resolve the case-variant spelling to the directory it names').toBe(true);
      expect(realpathSync.native(caseVariant)).toBe(realpathSync.native(physicalUserData));
      expect(realpathSync.native(junctionAlias)).toBe(realpathSync.native(physicalUserData));
      expect(realpathSync.native(differentPhysicalUserData)).not.toBe(realpathSync.native(physicalUserData));

      const physicalRoot = (clipboardPaste as ClipboardStorageNamespaceSeam).clipboardStorageRoot!(temporaryDirectory, physicalUserData);
      const caseRoot = (clipboardPaste as ClipboardStorageNamespaceSeam).clipboardStorageRoot!(temporaryDirectory, caseVariant);
      const junctionRoot = (clipboardPaste as ClipboardStorageNamespaceSeam).clipboardStorageRoot!(temporaryDirectory, junctionAlias);
      const differentRoot = (clipboardPaste as ClipboardStorageNamespaceSeam).clipboardStorageRoot!(temporaryDirectory, differentPhysicalUserData);

      expect(caseRoot).toBe(physicalRoot);
      expect(junctionRoot).toBe(physicalRoot);
      expect(differentRoot).not.toBe(physicalRoot);
    } finally {
      rmSync(sandbox, { recursive: true, force: true });
    }
  });
});

describe('desktop-windows-app runs the exact DACL acceptance before packaging', () => {
  it(`executes ${DACL_TEST} on windows-latest before package:win`, () => {
    const workflowPath = new URL('../../.github/workflows/desktop-windows-app.yml', import.meta.url);
    const workflow = parseWorkflow(readFileSync(workflowPath, 'utf8'));
    const jobs = Object.values(asMap(workflow.jobs)).map(asMap);
    const packagingJobs = jobs.filter((job) => asList(job.steps).map(asMap)
      .some((step) => /\bnpm\s+run\s+package:win\b/.test(asText(step.run))));

    expect(packagingJobs, 'desktop-windows-app.yml must contain exactly one package:win job').toHaveLength(1);
    const job = packagingJobs[0];
    expect(asText(job['runs-on']), 'the packaging job must provide real Windows DACL semantics').toBe('windows-latest');

    const steps = asList(job.steps).map(asMap);
    const packagingIndex = steps.findIndex((step) => /\bnpm\s+run\s+package:win\b/.test(asText(step.run)));
    const acceptanceIndexes = steps
      .map((step, index) => ({ index, command: asText(step.run).trim(), directory: asText(step['working-directory']).trim() }))
      .filter((step) => step.command === DACL_TEST_COMMAND && step.directory === 'desktop')
      .map((step) => step.index);

    expect(acceptanceIndexes, `add a desktop/ step running exactly \`${DACL_TEST_COMMAND}\``).toHaveLength(1);
    expect(acceptanceIndexes[0], 'the DACL acceptance must stop the job before installer packaging').toBeLessThan(packagingIndex);
  });
});

import { spawnSync } from 'node:child_process';
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { expect, it } from 'vitest';
import { embeddedSource } from './fixtures/pi-fullscreen-clipboard';

// Never set this gate on a developer login session. This test deliberately replaces the clipboard.
const gated = process.env.VC_ISOLATED_CLIPBOARD_ACCEPTANCE === 'I_OWN_THIS_ISOLATED_CLIPBOARD_SESSION';
it.skipIf(!gated)('R8: actual consumer selection reaches isolated OS clipboard four times, independently read back', () => {
  expect(['darwin', 'win32']).toContain(process.platform);
  const entry = process.env.VC_NATIVE_PI_ENTRY;
  const packageDir = process.env.VC_NATIVE_PI_PACKAGE_DIR;
  expect(entry, 'explicit unbundled dist/cli.js or actual pi~BUN.mjs entry required').toBeTruthy();
  expect(packageDir, 'explicit consumer package directory required').toBeTruthy();
  expect(path.isAbsolute(entry!)).toBe(true); expect(path.isAbsolute(packageDir!)).toBe(true);
  const work = mkdtempSync(path.resolve('tests/.native-clipboard-'));
  try {
    const home = path.join(work, 'home'); mkdirSync(home);
    writeFileSync(path.join(work, 'managed.ts'), embeddedSource());
    writeFileSync(path.join(work, 'probe.ts'), readFileSync(path.resolve('tests/fixtures/pi-fullscreen-native-probe.ts')));
    const env = {
      PATH: process.env.PATH, SystemRoot: process.env.SystemRoot, WINDIR: process.env.WINDIR,
      LC_ALL: 'C', // Deliberately hostile locale; LANG is absent.
      TEMP: work, TMP: work, TMPDIR: work, HOME: home, USERPROFILE: home,
      PI_CODING_AGENT_DIR: path.join(home, 'agent'), PI_PACKAGE_DIR: packageDir,
      PI_OFFLINE: '1', PI_TELEMETRY: '0', PI_SKIP_VERSION_CHECK: '1',
      VC_ISOLATED_CLIPBOARD_ACCEPTANCE: process.env.VC_ISOLATED_CLIPBOARD_ACCEPTANCE,
    };
    // --list-models awaits async extension factories, but needs no model, account or prompt.
    const result = spawnSync(process.execPath, [entry!, '--offline', '--no-extensions', '--no-skills', '--no-prompt-templates', '--no-themes', '--no-context-files', '-e', path.join(work, 'probe.ts'), '--list-models'], {
      cwd: work, env, encoding: 'utf8', timeout: 55000, maxBuffer: 2 * 1024 * 1024,
      stdio: ['ignore', 'pipe', 'pipe'],
    });
    // Pi can catch factory errors on stderr and still exit zero with a model table.
    // Keep bounded head/tail context; the probe sanitizes clipboard read failures/values.
    const context = (text: string | null): string => {
      const value = text ?? '';
      return value.length <= 8192 ? value : `${value.slice(0, 4096)}\n...[truncated]...\n${value.slice(-4096)}`;
    };
    const diagnostic = `Pi status=${result.status} signal=${result.signal} error=${context(result.error?.message ?? '')}\nstderr:\n${context(result.stderr)}\nstdout:\n${context(result.stdout)}`;
    expect(result.error, diagnostic).toBeUndefined();
    expect(result.status, diagnostic).toBe(0);
    expect(result.stdout?.includes('ASTRA_NATIVE_SELECTION_READBACK_OK_4'), `Required ASTRA_NATIVE_SELECTION_READBACK_OK_4\n${diagnostic}`).toBe(true);
  } finally { rmSync(work, { recursive: true, force: true }); }
}, 60000);

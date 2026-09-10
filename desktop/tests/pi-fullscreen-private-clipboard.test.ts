// All controls are fake: no compiler, private objects, Pi or clipboard on any host OS.
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { expect, it, vi } from 'vitest';
import { runPrivateWindowsClipboard, type NativeRequest } from './fixtures/pi-fullscreen-private-clipboard';
import type { spawnSync, SpawnSyncReturns } from 'node:child_process';

const result = (status: number | null, error?: Error): SpawnSyncReturns<string> => ({
  pid: 1, output: [], stdout: '', stderr: '', signal: null, status, error,
});
const request = (): NativeRequest => ({
  work: path.resolve("fake owned's temp"), node: path.resolve('portable node.exe'),
  args: [path.resolve('exact package/pi~BUN.mjs'), '--offline', '-e', path.resolve('probe with spaces.ts'), '', 'quote"slash\\'],
  env: {
    SystemRoot: path.resolve('fake Windows'), PATH: 'exact sparse PATH', LC_ALL: 'C',
    PI_PACKAGE_DIR: path.resolve('exact package'),
    VC_ISOLATED_CLIPBOARD_ACCEPTANCE: 'I_OWN_THIS_ISOLATED_CLIPBOARD_SESSION',
  },
});

it('compiles first, forwards exact entry/argv/sparse env, and returns only the child result', () => {
  const input = request(); const child = result(91); child.stdout = 'ASTRA_NATIVE_SELECTION_READBACK_OK_4';
  const spawn = vi.fn().mockReturnValueOnce(result(0)).mockReturnValueOnce(child);
  expect(runPrivateWindowsClipboard(input, spawn as typeof spawnSync)).toBe(child);
  expect(spawn).toHaveBeenCalledTimes(2);
  const [compiler, compileArgs, compileOptions] = spawn.mock.calls[0];
  expect(compiler).toBe(path.join(input.env.SystemRoot!, 'Microsoft.NET/Framework64/v4.0.30319/csc.exe'));
  expect(compileArgs).toEqual([
    '/nologo', '/target:exe', '/platform:x64', `/out:${path.join(input.work, 'private-clipboard.exe')}`,
    path.resolve('tests/fixtures/pi-fullscreen-private-clipboard.cs'),
  ]);
  expect(compileOptions).toMatchObject({ timeout: 30000, cwd: input.work, maxBuffer: 2 * 1024 * 1024 });
  const [executable, args, options] = spawn.mock.calls[1];
  expect(executable).toBe(path.join(input.work, 'private-clipboard.exe'));
  expect(args).toEqual([input.node, ...input.args]);
  expect(options).toMatchObject({ timeout: 60000, cwd: input.work, stdio: ['ignore', 'pipe', 'pipe'] });
  expect(options.env).toBe(input.env); expect(compileOptions.env).toBe(input.env);
});

it.each([result(1), result(null, new Error('PRIVATE_COMPILER_PAYLOAD'))])('compile failure is sanitized and never launches Node', (failed) => {
  const spawn = vi.fn().mockReturnValue(failed);
  expect(() => runPrivateWindowsClipboard(request(), spawn as typeof spawnSync)).toThrow(/^PRIVATE_CLIPBOARD_COMPILE_FAILED$/);
  expect(spawn).toHaveBeenCalledTimes(1);
});

it.each(['gate', 'root', 'entry', 'node'])('invalid %s fails before compilation', (mutation) => {
  const input = request();
  if (mutation === 'gate') delete input.env.VC_ISOLATED_CLIPBOARD_ACCEPTANCE;
  if (mutation === 'root') delete input.env.SystemRoot;
  if (mutation === 'entry') input.args[0] = 'relative.mjs';
  if (mutation === 'node') input.node = 'node';
  const spawn = vi.fn();
  expect(() => runPrivateWindowsClipboard(input, spawn as typeof spawnSync)).toThrow(/PRIVATE_CLIPBOARD_(GATE|INPUT)_REQUIRED/);
  expect(spawn).not.toHaveBeenCalled();
});

it('native safety contract retains create-only ACL/STA/startup/job/reap order, without guards or clipboard APIs', () => {
  // Structural mutation tripwires, NOT substitutes for Windows actual-bundle qualification.
  const source = readFileSync(path.resolve('tests/fixtures/pi-fullscreen-private-clipboard.cs'), 'utf8');
  const before = (first: string, second: string): void => {
    expect(source).toContain(first); expect(source).toContain(second);
    expect(source.indexOf(first)).toBeLessThan(source.indexOf(second));
  };
  expect(source).toContain('CreateWindowStationW(s,1,0x000F037F,ref sa)');
  expect(source).toContain('new RawSecurityDescriptor("D:P(A;;GA;;;"+WindowsIdentity.GetCurrent().User.Value+")")');
  expect(source).toContain('descriptor=sd,inherit=1');
  expect(source).toContain('worker.SetApartmentState(System.Threading.ApartmentState.STA)');
  before('Check(SetThreadDesktop(desktop)', 'RunChild(s,d,state,args)');
  expect(source).toContain('Verify(s,d,"LAUNCH")');
  expect(source).toContain('desktop=s+"\\\\"+d');
  expect(source).toContain('0x08000404,IntPtr.Zero,root,ref si,out pi');
  expect(source).toContain('Marshal.WriteInt32(data,16,0x2000)');
  expect(source).toContain('AssignProcessToJobObject(lifetimeJob,Process.GetCurrentProcess().Handle)');
  before('AssignProcessToJobObject(job,pi.process)', 'ResumeThread(pi.thread)');
  expect(source).toContain('TerminateJobObject(job,99)');
  expect(source).toContain('Marshal.ReadInt32(data,40)==0');
  before('ReapJob(job); reaped=true;', 'CloseHandle(output),"CLOSE_STDOUT"');
  before('CloseHandle(error),"CLOSE_STDERR"', 'Emit(stderr,true)');
  expect(source).not.toMatch(/OpenWindowStation|OpenDesktop|SwitchDesktop|Clipboard\.|guard-config|ASTRA_NATIVE_SELECTION_READBACK_OK_4/);
});

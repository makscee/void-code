import { describe, expect, it, vi } from 'vitest';
import { verifyWindowsSignatures } from '../scripts/windows-signing-check.mjs';

describe('Windows package signer qualification', () => {
  it('cannot qualify from a source-declared signer when the operator expectation is absent', () => {
    const execute = vi.fn();
    expect(() => verifyWindowsSignatures({
      installer: 'C:\\frozen\\installer.exe',
      appExecutable: 'C:\\frozen\\Void Code.exe',
      expectedPublisher: undefined,
      powershell: 'powershell.exe',
      execute,
    })).toThrow('Windows signing capability unavailable');
    expect(execute).not.toHaveBeenCalled();
  });

  it('passes paths and expected signer through environment to a static PowerShell command', () => {
    const execute = vi.fn(() => 'ok\n');
    verifyWindowsSignatures({
      installer: 'C:\\frozen & literal\\installer.exe',
      appExecutable: 'C:\\frozen & literal\\Void Code.exe',
      expectedPublisher: 'Test Publisher',
      powershell: 'powershell.exe',
      execute,
    });
    const [, args, options] = execute.mock.calls[0];
    const command = args.join(' ');
    expect(command).toContain('Get-AuthenticodeSignature -LiteralPath $file');
    expect(command).toContain("$signature.Status -ne 'Valid'");
    expect(command).toContain('X509NameType]::SimpleName');
    expect(command).toContain('StringComparison]::Ordinal');
    expect(command).not.toContain('Test Publisher');
    expect(command).not.toContain('frozen & literal');
    expect(options.env).toMatchObject({
      VC_DESKTOP_EXPECTED_PUBLISHER: 'Test Publisher',
      VC_DESKTOP_SIGNATURE_INSTALLER: 'C:\\frozen & literal\\installer.exe',
      VC_DESKTOP_SIGNATURE_APP: 'C:\\frozen & literal\\Void Code.exe',
    });
    expect(options.stdio).toEqual(['ignore', 'pipe', 'ignore']);
  });

  it.each([[''], ['not-ok'], [new Error('certificate detail that must not escape')]])('fails closed with one coarse error for absent or invalid signing capability', (result) => {
    const execute = vi.fn(() => { if (result instanceof Error) throw result; return result; });
    expect(() => verifyWindowsSignatures({ installer: 'a', appExecutable: 'b', expectedPublisher: 'Test Publisher', powershell: 'powershell.exe', execute })).toThrow(/^Windows signing capability unavailable$/);
  });
});

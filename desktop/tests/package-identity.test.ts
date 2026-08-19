import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const read = (relative: string): string => readFileSync(new URL(`../${relative}`, import.meta.url), 'utf8');

describe('decided pilot package identity', () => {
  it('uses the new Void Code 0.1.3-beta.5 identity without relabeling frozen predecessors', () => {
    const packageJson = JSON.parse(read('package.json')) as {
      version: string;
      description: string;
      build: { appId: string; productName: string; nsis: { artifactName: string } };
    };
    const packageLock = JSON.parse(read('package-lock.json')) as { version: string; packages: Record<string, { version?: string }> };
    expect(packageJson).toMatchObject({
      version: '0.1.3-beta.5',
      description: 'Guided Void Code desktop pilot',
      build: {
        appId: 'works.voidcode.desktop',
        productName: 'Void Code',
        nsis: { artifactName: 'Void-Code-${version}-windows-${arch}.${ext}' },
      },
    });
    expect(packageLock.version).toBe('0.1.3-beta.5');
    expect(packageLock.packages['']?.version).toBe('0.1.3-beta.5');
    expect(read('src/renderer/index.html')).toContain('<title>Void Code</title>');
    expect(packageJson).toMatchObject({
      dependencies: { 'electron-updater': '6.8.9', '@vscode/l10n': '0.0.18', 'js-yaml': '4.1.0' },
      devDependencies: { '@types/js-yaml': '4.0.9', electron: '41.10.3' },
      build: {
        publish: [{ provider: 'generic', url: 'https://vc.makscee.ru/download/windows/' }],
        win: { target: 'nsis' },
        nsis: { perMachine: false },
      },
    });
    const win = (packageJson as { build: { win: Record<string, unknown> } }).build.win;
    expect(win).not.toHaveProperty('publisherName');
    expect(win).not.toHaveProperty('signAndEditExecutable');
  });

  it('keeps package consumers deterministic without changing private runtime identity', () => {
    const privateRuntime = JSON.parse(read('runtime/pi/package.json')) as { name: string; version: string };
    expect(privateRuntime).toEqual(expect.objectContaining({ name: 'void-code-private-pi-runtime', version: '0.0.1' }));

    const smoke = read('scripts/packaged-smoke.mjs');
    expect(smoke).toContain("release/mac-arm64/Void Code.app");
    expect(smoke).toContain("identity.version !== '0.1.3-beta.5'");
    expect(smoke).toContain("identity.appId !== 'works.voidcode.desktop'");
    const macPtyCheck = read('scripts/mac-pty-check.mjs');
    expect(macPtyCheck).toContain("release/mac-arm64/Void Code.app");
    expect(macPtyCheck).toContain("const electronVersion = packageJson.devDependencies?.electron");
    expect(macPtyCheck).toContain("installedElectronVersion !== electronVersion");
    expect(macPtyCheck).toContain("electron: electronVersion");
    expect(macPtyCheck).not.toMatch(/electron:\s*['"]\d+\.\d+\.\d+['"]/);
    expect(read('scripts/production-terminal-check.mjs')).toContain("release/mac-arm64/Void Code.app/Contents/MacOS/Void Code");
    const windowsCheck = read('scripts/windows-package-check.mjs');
    expect(windowsCheck).toContain('sourcePackage.version');
    expect(windowsCheck).toContain("electronVersion !== '41.10.3'");
    expect(windowsCheck).toContain('VC_DESKTOP_EXPECTED_PUBLISHER');
    expect(windowsCheck).not.toContain('sourcePackage.build?.win?.publisherName');
    expect(windowsCheck).not.toContain("electron: '39.2.6'");
    expect(read('src/main/electron-updater-adapter.ts')).not.toContain('verifyUpdateCodeSignature = false');
  });

  it('binds the fixed signer policy to the beta.4 to beta.5 transition', () => {
    const signer = readFileSync(new URL('../../scripts/fland-internal-beta-signer.py', import.meta.url), 'utf8');
    expect(signer).toContain("request['fromVersion'] != '0.1.3-beta.4'");
    expect(signer).toContain("value['version'] != '0.1.3-beta.5'");
    expect(signer).toContain("value['sequence'] != 5");
    expect(signer).toContain("PREDECESSOR_SHA256 = '6e2073dd8b6dae2f07adf915d6ea895f2e33e6362851c6777de6067a456d08fd'");
    expect(signer).toContain("request['fromArtifactSha256'] != PREDECESSOR_SHA256");
    expect(signer).toContain('initialization disabled: provision the enrolled key only through the attended external ceremony');
    expect(signer).not.toContain("value['version'] != '0.1.3-beta.4'");
    expect(signer).not.toContain("'genpkey'");
  });

  it('retires the frozen review-failed 0.1.1 identity and reserves the post-fix candidate identity', () => {
    const contract = read('docs/stable-update-contract.md');
    expect(contract).toContain('Stable-track reference only');
    expect(contract).toContain('current source/package identity is `0.1.3-beta.5`');
    expect(contract).toContain('0.1.1: `RETIRED_INTERNAL_REVIEW_FAILED`');
    expect(contract).toContain('unsigned');
    expect(contract).toContain('S1');
    expect(contract).toContain('never publish or accept');
    expect(contract).toContain('Only 0.1.2 may become the post-fix candidate');

    const fixture = read('docs/fixtures/stable-v1-accepted.template.json');
    expect(fixture).toContain('Void-Code-0.1.2-windows-x64.exe');
    expect(fixture).toContain('desktop-v0.1.2');
  });
});

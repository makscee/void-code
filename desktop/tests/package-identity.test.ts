import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const read = (relative: string): string => readFileSync(new URL(`../${relative}`, import.meta.url), 'utf8');

describe('decided pilot package identity', () => {
  it('uses Void Code 0.1.0 in authoritative package and installed application metadata', () => {
    const packageJson = JSON.parse(read('package.json')) as {
      version: string;
      description: string;
      build: { appId: string; productName: string; nsis: { artifactName: string } };
    };
    const packageLock = JSON.parse(read('package-lock.json')) as { version: string; packages: Record<string, { version?: string }> };
    expect(packageJson).toMatchObject({
      version: '0.1.0',
      description: 'Guided Void Code desktop pilot',
      build: {
        appId: 'works.voidcode.desktop',
        productName: 'Void Code',
        nsis: { artifactName: 'Void-Code-${version}-windows-${arch}.${ext}' },
      },
    });
    expect(packageLock.version).toBe('0.1.0');
    expect(packageLock.packages['']?.version).toBe('0.1.0');
    expect(read('src/renderer/index.html')).toContain('<title>Void Code</title>');
  });

  it('keeps package consumers deterministic without changing private runtime identity', () => {
    const privateRuntime = JSON.parse(read('runtime/pi/package.json')) as { name: string; version: string };
    expect(privateRuntime).toEqual(expect.objectContaining({ name: 'void-code-private-pi-runtime', version: '0.0.1' }));

    const smoke = read('scripts/packaged-smoke.mjs');
    expect(smoke).toContain("release/mac-arm64/Void Code.app");
    expect(smoke).toContain("identity.version !== '0.1.0'");
    expect(smoke).toContain("identity.appId !== 'works.voidcode.desktop'");
    const macPtyCheck = read('scripts/mac-pty-check.mjs');
    expect(macPtyCheck).toContain("release/mac-arm64/Void Code.app");
    expect(macPtyCheck).toContain("const electronVersion = packageJson.devDependencies?.electron");
    expect(macPtyCheck).toContain("installedElectronVersion !== electronVersion");
    expect(macPtyCheck).toContain("electron: electronVersion");
    expect(macPtyCheck).not.toMatch(/electron:\s*['"]\d+\.\d+\.\d+['"]/);
    expect(read('scripts/production-terminal-check.mjs')).toContain("release/mac-arm64/Void Code.app/Contents/MacOS/Void Code");
    expect(read('scripts/windows-package-check.mjs')).toContain("Void-Code-0.1.0-windows-x64.exe");
  });
});

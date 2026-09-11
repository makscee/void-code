import { spawn } from 'node:child_process';
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { createRequire } from 'node:module';
import os from 'node:os';
import path from 'node:path';
import { build } from 'esbuild';
import { expect, it } from 'vitest';

// Native Chromium modifiers on the host, NOT Windows OS-native menu/Alt acceptance.
// Synthetic bridge proves renderer routing only: no Pi/editor, inference, auth startup,
// credential/workspace access, clipboard operations or foreground/global keyboard use.
it('cycles active tabs through the actual entry/page and real hidden Electron+xterm', async () => {
  const root = mkdtempSync(path.join(os.tmpdir(), 'vc-native-keys-'));
  try {
    for (const dir of ['home', 'tmp', 'userData', 'sessionData']) mkdirSync(path.join(root, dir));
    const bundle = await build({ entryPoints: ['src/renderer/index.ts'], bundle: true, format: 'esm', platform: 'browser', target: 'chrome130', loader: { '.woff': 'file', '.woff2': 'file' }, outfile: path.join(root, 'index.js'), metafile: true, logLevel: 'silent' });
    // No aliases, replacements or altered Terminal options: esbuild consumes the shipped entry,
    // terminal stack and installed real xterm; the actual HTML is copied byte-for-byte.
    for (const source of ['src/renderer/index.ts', 'src/renderer/terminal-stack.ts', 'src/renderer/clipboard-shortcuts.ts']) expect(bundle.metafile.inputs).toHaveProperty(source);
    expect(Object.keys(bundle.metafile.inputs).some(file => file.includes('node_modules/@xterm/xterm/'))).toBe(true);
    copyFileSync('src/renderer/index.html', path.join(root, 'index.html'));
    expect(readFileSync(path.join(root, 'index.html'))).toEqual(readFileSync('src/renderer/index.html'));
    for (const name of ['main', 'preload']) await build({ entryPoints: [`tests/fixtures/desktop-keyboard-native-${name}.ts`], bundle: true, format: 'cjs', platform: 'node', target: 'node22', external: ['electron'], outfile: path.join(root, `${name}.cjs`), logLevel: 'silent' });
    const electron = createRequire(import.meta.url)('electron') as string;
    const env: NodeJS.ProcessEnv = { HOME: path.join(root, 'home'), USERPROFILE: path.join(root, 'home'), TMPDIR: path.join(root, 'tmp'), TMP: path.join(root, 'tmp'), TEMP: path.join(root, 'tmp'), XDG_CONFIG_HOME: path.join(root, 'home'), XDG_CACHE_HOME: path.join(root, 'home') };
    for (const key of ['SystemRoot', 'WINDIR', 'DISPLAY', 'WAYLAND_DISPLAY', 'XDG_RUNTIME_DIR']) if (process.env[key]) env[key] = process.env[key];
    const completed = await new Promise<{ code: number | null; output: string }>((resolve, reject) => {
      const args = process.platform === 'linux' ? ['--ozone-platform=headless', '--disable-gpu'] : [];
      const child = spawn(electron, [...args, path.join(root, 'main.cjs'), `--fixture-root=${root}`], { cwd: path.resolve('.'), env, stdio: ['ignore', 'pipe', 'pipe'] });
      let output = '';
      child.stdout.on('data', chunk => { output += chunk; }); child.stderr.on('data', chunk => { output += chunk; });
      const timer = setTimeout(() => child.kill('SIGKILL'), 12_000);
      child.once('error', error => { clearTimeout(timer); reject(error); });
      child.once('close', code => { clearTimeout(timer); resolve({ code, output }); });
    });
    expect(completed.code, completed.output).toBe(0);
    const result = JSON.parse(readFileSync(path.join(root, 'result.json'), 'utf8'));
    console.info('native renderer keyboard receipt', JSON.stringify(result));
    expect(result.electron).toBe('41.10.3'); expect(result.platform).toBe(process.platform);
    expect(result.initial.focus).toBe(true); expect(result.initial.terminals).toBe(1);
    expect(result.initial.starts).toEqual(['A']); expect(result.initial.inputs).toEqual([]);
    const expected = ['B', 'A', 'C', 'A', 'B', 'C', 'A'];
    const inputs: { sessionId: string; data: string }[] = [];
    for (const [index, target] of expected.entries()) {
      const { navigation, typed } = result.steps[index];
      expect.soft(navigation.selects, `CtrlTab step ${index}: select ${target}`).toEqual(expected.slice(0, index + 1));
      expect.soft(navigation.selected, `step ${index}: actual selected DOM tab`).toBe(target);
      expect.soft(navigation.focus, `step ${index}: target textarea owns focus`).toBe(true);
      expect.soft(navigation.inputs, `step ${index}: shortcut emits no bytes`).toEqual(inputs);
      inputs.push({ sessionId: target, data: 'x' });
      expect.soft(typed.inputs, `step ${index}: typing reaches only ${target}`).toEqual(inputs);
      expect.soft(typed.starts).not.toContain('R');
    }
    expect(result.events).toHaveLength(14);
    for (const event of result.events) { expect.soft(event.trusted).toBe(true); expect.soft(event.ctrl).toBe(true); expect.soft(event.prevented, `${event.type} consumed`).toBe(true); }
  } finally { rmSync(root, { recursive: true, force: true }); }
}, 20_000);

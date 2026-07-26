import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

describe('packaged renderer entry', () => {
  it('uses the production module bundler and official terminal construction path', () => {
    const packageJson = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8')) as { scripts: Record<string, string> };
    const html = readFileSync(new URL('../src/renderer/index.html', import.meta.url), 'utf8');
    const stack = readFileSync(new URL('../src/renderer/terminal-stack.ts', import.meta.url), 'utf8');
    const renderer = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');
    const probe = readFileSync(new URL('../scripts/production-terminal-check.mjs', import.meta.url), 'utf8');
    expect(packageJson.scripts['bundle:renderer']).toContain('esbuild src/renderer/index.ts --bundle --format=esm');
    expect(html).toContain('<script type="module" src="index.js"></script>');
    expect(html).not.toContain('xterm.js');
    expect(stack).toContain("from '@xterm/xterm'");
    expect(stack).toContain("import '@xterm/xterm/css/xterm.css'");
    expect(renderer).not.toContain('data.match(');
    expect(renderer).not.toContain('\\x1b\\[');
    expect(probe).toContain("runProbe('missing-font', 'missing-font')");
    expect(probe).toContain("runProbe('palette-collapse', 'palette-collapse')");
  });
});

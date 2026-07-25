import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';

describe('packaged renderer entry', () => {
  it('emits as a browser script without a CommonJS exports dependency', () => {
    const source = readFileSync(new URL('../src/renderer/index.ts', import.meta.url), 'utf8');
    const output = ts.transpileModule(source, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS } }).outputText;
    expect(output).not.toContain('Object.defineProperty(exports');
    expect(output).not.toMatch(/\brequire\s*\(/);
  });
});

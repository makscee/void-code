import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';

type RendererPlatform = 'darwin' | 'win32' | 'other';
type RendererPlatformModule = {
  detectRendererPlatform(userAgent: string): RendererPlatform;
};

async function rendererPlatformDetector(): Promise<RendererPlatformModule> {
  const module = await import(new URL('../src/renderer/platform.ts', import.meta.url).href).catch(() => undefined);
  expect(module, 'renderer platform detection must be a DOM-free production module').toBeDefined();
  return module as RendererPlatformModule;
}

describe('renderer platform detection', () => {
  it.each([
    ['Apple Silicon Electron', 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Void Code/0.1.0 Chrome/142.0.7444.175 Electron/39.0.0 Safari/537.36', 'darwin'],
    ['Intel Electron', 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Void Code/0.1.0 Chrome/130.0.6723.191 Electron/33.2.0 Safari/537.36', 'darwin'],
    ['Windows Electron', 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Void Code/0.1.0 Chrome/142.0.7444.175 Electron/39.0.0 Safari/537.36', 'win32'],
    ['Linux Electron', 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Void Code/0.1.0 Chrome/142.0.7444.175 Electron/39.0.0 Safari/537.36', 'other'],
    ['unrecognised user agent', 'Void Code test harness', 'other'],
  ] satisfies Array<[string, string, RendererPlatform]>)('%s maps to its expected renderer platform', async (_label, userAgent, platform) => {
    expect((await rendererPlatformDetector()).detectRendererPlatform(userAgent)).toBe(platform);
  });
});

function rendererSource(): ts.SourceFile {
  const file = new URL('../src/renderer/index.ts', import.meta.url);
  return ts.createSourceFile(file.pathname, readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
}

describe('renderer clipboard platform composition', () => {
  it('derives the production clipboard platform from the tested user-agent detector', () => {
    const source = rendererSource();
    const detectorImport = source.statements.find((statement): statement is ts.ImportDeclaration => ts.isImportDeclaration(statement)
      && ts.isStringLiteral(statement.moduleSpecifier) && statement.moduleSpecifier.text === './platform');
    const importedNames = detectorImport?.importClause?.namedBindings && ts.isNamedImports(detectorImport.importClause.namedBindings)
      ? detectorImport.importClause.namedBindings.elements.map((element) => element.name.text)
      : [];
    expect(importedNames).toContain('detectRendererPlatform');

    const declaration = source.statements
      .filter(ts.isVariableStatement)
      .flatMap((statement) => statement.declarationList.declarations)
      .find((candidate) => ts.isIdentifier(candidate.name) && candidate.name.text === 'rendererPlatform');
    expect(declaration, 'rendererPlatform must be declared for terminal clipboard wiring').toBeDefined();
    expect(declaration?.initializer && ts.isCallExpression(declaration.initializer), 'rendererPlatform must come from the shared detector, not an inline platform conditional').toBe(true);
    if (!declaration?.initializer || !ts.isCallExpression(declaration.initializer)) return;

    expect(ts.isIdentifier(declaration.initializer.expression) && declaration.initializer.expression.text).toBe('detectRendererPlatform');
    expect(declaration.initializer.arguments).toHaveLength(1);
    const [userAgent] = declaration.initializer.arguments;
    expect(ts.isPropertyAccessExpression(userAgent)
      && ts.isIdentifier(userAgent.expression) && userAgent.expression.text === 'navigator'
      && userAgent.name.text === 'userAgent').toBe(true);
  });
});

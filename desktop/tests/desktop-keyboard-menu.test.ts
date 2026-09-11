import { readFileSync } from 'node:fs';
import path from 'node:path';
import ts from 'typescript';
import { expect, it, vi } from 'vitest';

// Extract executable production code, not a parallel factory or source-regex policy check.
it.each(['win32', 'linux', 'darwin'])('K1: actual createWindow removes rather than hides Alt menu before loading on %s', async platform => {
  const url = new URL('../src/main/index.ts', import.meta.url);
  const source = ts.createSourceFile(url.pathname, readFileSync(url, 'utf8'), ts.ScriptTarget.Latest, true);
  const factories = source.statements.filter((node): node is ts.FunctionDeclaration => ts.isFunctionDeclaration(node) && node.name?.text === 'createWindow');
  expect(factories, 'provenance: exactly one actual main createWindow').toHaveLength(1);
  const factory = factories[0].getText(source);
  const identifiers = new Set<string>();
  const visit = (node: ts.Node): void => { if (ts.isIdentifier(node)) identifiers.add(node.text); ts.forEachChild(node, visit); };
  visit(factories[0]);
  let applicationMenu: unknown = { defaultElectronMenu: true };
  const observations: Array<{ at: string; attached: boolean }> = [];
  const windows: Window[] = [];
  class Window {
    menu: unknown = applicationMenu;
    webContents = { on: vi.fn(), setWindowOpenHandler: vi.fn() };
    constructor(options: unknown) { void options; windows.push(this); }
    on = vi.fn();
    setMenu(menu: unknown): void { this.menu = menu; }
    removeMenu(): void { this.menu = null; }
    setMenuBarVisibility = vi.fn(); // hiding is deliberately not removal
    setAutoHideMenuBar = vi.fn();
    isDestroyed(): boolean { return false; }
    async loadFile(file: string): Promise<void> { observations.push({ at: path.basename(file), attached: this.menu !== null }); }
    show(): void { observations.push({ at: 'show', attached: this.menu !== null }); }
    focus = vi.fn();
  }
  const Menu = { setApplicationMenu: vi.fn((menu: unknown) => { applicationMenu = menu; windows.forEach(window => { window.menu = menu; }); }), getApplicationMenu: () => applicationMenu };
  const bindings: Record<string, unknown> = { BrowserWindow: Window, Menu, process: { platform }, __dirname: '/synthetic/main', headlessProbe: undefined, LOADING_PAGE: 'loading.html', missingRendererTest: false, ownedCleanup: { cleanup: vi.fn() } };
  // Resolve actual imports referenced by the factory, including a future extracted menu helper.
  for (const statement of source.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier)) continue;
    const specifier = statement.moduleSpecifier.text;
    if (specifier === 'electron' || statement.importClause?.isTypeOnly) continue;
    const names = statement.importClause?.namedBindings;
    if (names && ts.isNamedImports(names)) {
      const used = names.elements.filter(name => !name.isTypeOnly && identifiers.has(name.name.text));
      if (!used.length) continue;
      const module = await import(specifier.startsWith('.') ? new URL(`${specifier}.ts`, url).href : specifier);
      for (const name of used) bindings[name.name.text] = module[name.propertyName?.text ?? name.name.text];
    } else if (statement.importClause?.name && identifiers.has(statement.importClause.name.text)) {
      bindings[statement.importClause.name.text] = (await import(specifier)).default;
    }
  }
  const js = ts.transpileModule(factory, { compilerOptions: { target: ts.ScriptTarget.ES2022 } }).outputText;
  const create = new Function(...Object.keys(bindings), `let mainWindow; ${js}; return createWindow;`)(...Object.values(bindings));
  const startup = await create();
  await startup.loadLoadingPage(); await startup.loadApplicationPage();
  expect(observations, 'attached default menu remains Alt-activatable even when hidden').toEqual([
    { at: 'loading.html', attached: platform === 'darwin' }, { at: 'show', attached: platform === 'darwin' },
    { at: 'index.html', attached: platform === 'darwin' }, { at: 'show', attached: platform === 'darwin' },
  ]);
  if (platform === 'darwin') expect(Menu.setApplicationMenu).not.toHaveBeenCalled();
});

// Test-only source hooks: execute actual consumer code, never a proxy facsimile.
// Avoid importing InteractiveMode's session/theme/keybinding initialization graph.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import ts from 'typescript';

export function consumerHooks(file: string, suppliedSource?: string, extraMethods: readonly string[] = []) {
  const source = suppliedSource ?? readFileSync(file, 'utf8');
  const tree = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
  const factories: ts.FunctionDeclaration[] = [];
  const methods = new Map<string, string>();
  let boundByConsumer = false;
  function visit(node: ts.Node) {
    if (ts.isFunctionDeclaration(node) && /^createInteractiveTuiReference\d*$/.test(node.name?.text ?? '')) factories.push(node);
    const className = ts.isClassDeclaration(node) ? node.name?.text ?? ''
      : ts.isClassExpression(node) && ts.isVariableDeclaration(node.parent) ? node.parent.name.getText(tree)
        : ts.isClassExpression(node) && ts.isBinaryExpression(node.parent)
          && node.parent.operatorToken.kind === ts.SyntaxKind.EqualsToken ? node.parent.left.getText(tree) : '';
    if ((ts.isClassDeclaration(node) || ts.isClassExpression(node)) && /^InteractiveMode\d*$/.test(className)) {
      for (const member of node.members) {
        if (ts.isConstructorDeclaration(member)) {
          boundByConsumer = /this\.ui\s*=\s*(?:createInteractiveTuiReference\d*|\(0,\s*import_tui_renderer\d*\.createInteractiveTuiReference\))\(\(\)\s*=>\s*this\.renderer\)/.test(member.getText(tree));
        }
        if (ts.isMethodDeclaration(member) && ['setExtensionWidget', 'clearExtensionWidgets', ...extraMethods].includes(member.name.getText(tree))) {
          methods.set(member.name.getText(tree), member.getText(tree));
        }
      }
    }
    ts.forEachChild(node, visit);
  }
  visit(tree);
  let factoryTree = tree;
  if (factories.length === 0 && /createInteractiveTuiReference/.test(source) && /class\s+\w*InteractiveMode/.test(source)) {
    // Unbundled 0.87 moved the factory into its sibling module. A single-file
    // bundle already contains that same function: never count the source tree
    // and a separately installed copy together (especially on Windows).
    const rendererFile = suppliedSource === undefined
      ? path.join(path.dirname(file), 'tui-renderer.js')
      : path.resolve('runtime/pi/node_modules/@earendil-works/pi-coding-agent/dist/modes/interactive/tui-renderer.js');
    const rendererTree = ts.createSourceFile(rendererFile, readFileSync(rendererFile, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
    function visitRenderer(node: ts.Node) {
      if (ts.isFunctionDeclaration(node) && /^createInteractiveTuiReference\d*$/.test(node.name?.text ?? '')) factories.push(node);
      ts.forEachChild(node, visitRenderer);
    }
    visitRenderer(rendererTree);
    factoryTree = rendererTree;
  }
  assert.equal(factories.length, 1, 'consumer must contain one actual reference factory');
  assert.ok(boundByConsumer, 'actual InteractiveMode constructor must acquire this factory over this.renderer');
  const declaration = factories[0];
  assert.ok(declaration.body);
  const factory = `function ${declaration.name!.text}(${declaration.parameters.map((p) => p.getText(factoryTree)).join(',')}) ${declaration.body.getText(factoryTree)}`;
  return { file, sha256: createHash('sha256').update(source).digest('hex'), factory, name: declaration.name!.text, methods };
}

export function referenceFactory<T extends object>(file: string): (getTui: () => T) => T {
  const hooks = consumerHooks(file);
  return new Function(`${hooks.factory}; return ${hooks.name};`)() as (getTui: () => T) => T;
}

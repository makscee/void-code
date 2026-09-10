// Test-only source hooks: execute actual consumer code, never a proxy facsimile.
// Avoid importing InteractiveMode's session/theme/keybinding initialization graph.
import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import ts from 'typescript';

export function consumerHooks(file: string, source = readFileSync(file, 'utf8')) {
  const tree = ts.createSourceFile(file, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
  const factories: ts.FunctionDeclaration[] = [];
  const methods = new Map<string, string>();
  let boundByConsumer = false;
  function visit(node: ts.Node) {
    if (ts.isFunctionDeclaration(node) && /^createInteractiveTuiReference\d*$/.test(node.name?.text ?? '')) factories.push(node);
    const className = (ts.isClassDeclaration(node) || ts.isClassExpression(node))
      ? node.name?.text ?? (ts.isVariableDeclaration(node.parent) ? node.parent.name.getText(tree)
        : ts.isBinaryExpression(node.parent) ? node.parent.left.getText(tree) : '') : '';
    if ((ts.isClassDeclaration(node) || ts.isClassExpression(node)) && /^InteractiveMode\d*$/.test(className)) {
      for (const member of node.members) {
        if (ts.isConstructorDeclaration(member)) {
          boundByConsumer = /this\.ui\s*=\s*createInteractiveTuiReference\d*\(\(\)\s*=>\s*this\.renderer\)/.test(member.getText(tree));
        }
        if (ts.isMethodDeclaration(member) && ['setExtensionWidget', 'clearExtensionWidgets'].includes(member.name.getText(tree))) {
          methods.set(member.name.getText(tree), member.getText(tree));
        }
      }
    }
    ts.forEachChild(node, visit);
  }
  visit(tree);
  assert.equal(factories.length, 1, 'consumer must contain one actual reference factory');
  assert.ok(boundByConsumer, 'actual InteractiveMode constructor must acquire this factory over this.renderer');
  const declaration = factories[0];
  assert.ok(declaration.body);
  const factory = `function ${declaration.name!.text}(${declaration.parameters.map((p) => p.getText(tree)).join(',')}) ${declaration.body.getText(tree)}`;
  return { file, sha256: createHash('sha256').update(source).digest('hex'), factory, name: declaration.name!.text, methods };
}

export function referenceFactory<T extends object>(file: string): (getTui: () => T) => T {
  const hooks = consumerHooks(file);
  return new Function(`${hooks.factory}; return ${hooks.name};`)() as (getTui: () => T) => T;
}

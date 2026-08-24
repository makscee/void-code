import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';
import { IPC } from '../src/shared/preload-contract';

const notifications = new Set([IPC.subscribe, IPC.unsubscribe]);

function registeredHandlers(source: ts.SourceFile): Map<string, ts.ArrowFunction | ts.FunctionExpression> {
  const handlers = new Map<string, ts.ArrowFunction | ts.FunctionExpression>();
  function visit(node: ts.Node): void {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression)
      && ts.isIdentifier(node.expression.expression) && node.expression.expression.text === 'ipcMain'
      && ['handle', 'on'].includes(node.expression.name.text)) {
      const [channel, handler] = node.arguments;
      if (channel && ts.isPropertyAccessExpression(channel) && ts.isIdentifier(channel.expression)
        && channel.expression.text === 'IPC' && handler
        && (ts.isArrowFunction(handler) || ts.isFunctionExpression(handler))) {
        handlers.set(channel.name.text, handler);
      }
    }
    ts.forEachChild(node, visit);
  }
  visit(source);
  return handlers;
}

function firstEffectIsGuard(handler: ts.ArrowFunction | ts.FunctionExpression): boolean {
  if (!ts.isBlock(handler.body) || handler.body.statements.length === 0) return false;
  let statement = handler.body.statements[0];
  if (ts.isTryStatement(statement)) statement = statement.tryBlock.statements[0];
  return Boolean(statement && ts.isExpressionStatement(statement)
    && ts.isCallExpression(statement.expression)
    && ts.isIdentifier(statement.expression.expression)
    && statement.expression.expression.text === 'assertRenderer');
}

describe('IPC authority wiring', () => {
  it('registers every request channel with an authority guard as its first effect', () => {
    const text = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');
    const source = ts.createSourceFile('index.ts', text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
    const handlers = registeredHandlers(source);

    for (const [name, channel] of Object.entries(IPC)) {
      if ([IPC.output, IPC.exit, IPC.lifecycle, IPC.authLoginEvent].includes(channel)) continue;
      const handler = handlers.get(name);
      expect(handler, `${channel} is structurally registered`).toBeDefined();
      expect(firstEffectIsGuard(handler!), `${channel} guards before effects`).toBe(true);
      const registration = handler!.parent;
      expect(ts.isCallExpression(registration) && ts.isPropertyAccessExpression(registration.expression)
        ? registration.expression.name.text
        : undefined, `${channel} uses the expected registration API`).toBe(notifications.has(channel) ? 'on' : 'handle');
    }
  });
});

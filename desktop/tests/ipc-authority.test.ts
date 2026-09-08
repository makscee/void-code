import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { describe, expect, it } from 'vitest';
import { IPC } from '../src/shared/preload-contract';

const notifications = new Set([IPC.subscribe, IPC.unsubscribe]);
const delegatedClipboardChannels = ['clipboardRead', 'clipboardWrite'] as const;

type Handler = ts.ArrowFunction | ts.FunctionExpression;
type Registration = { api: string; guarded: boolean };

function directRegistrations(root: ts.Node): Map<string, Registration[]> {
  const registrations = new Map<string, Registration[]>();
  function visit(node: ts.Node): void {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression)
      && ts.isIdentifier(node.expression.expression) && node.expression.expression.text === 'ipcMain'
      && ['handle', 'on'].includes(node.expression.name.text)) {
      const [channel, handler] = node.arguments;
      if (channel && ts.isPropertyAccessExpression(channel) && ts.isIdentifier(channel.expression)
        && channel.expression.text === 'IPC' && handler
        && (ts.isArrowFunction(handler) || ts.isFunctionExpression(handler))) {
        const current = registrations.get(channel.name.text) ?? [];
        current.push({ api: node.expression.name.text, guarded: firstEffectIsGuard(handler) });
        registrations.set(channel.name.text, current);
      }
    }
    ts.forEachChild(node, visit);
  }
  visit(root);
  return registrations;
}

function firstEffectIsGuard(handler: Handler): boolean {
  if (!ts.isBlock(handler.body) || handler.body.statements.length === 0) return false;
  let statement = handler.body.statements[0];
  if (ts.isTryStatement(statement)) statement = statement.tryBlock.statements[0];
  return Boolean(statement && ts.isExpressionStatement(statement)
    && ts.isCallExpression(statement.expression)
    && ts.isIdentifier(statement.expression.expression)
    && statement.expression.expression.text === 'assertRenderer');
}

function propertyExpression(object: ts.ObjectLiteralExpression | undefined, name: string): ts.Expression | undefined {
  const property = object?.properties.find((item) => (ts.isPropertyAssignment(item) || ts.isShorthandPropertyAssignment(item))
    && (ts.isIdentifier(item.name) || ts.isStringLiteral(item.name)) && item.name.text === name);
  if (property && ts.isPropertyAssignment(property)) return property.initializer;
  return property && ts.isShorthandPropertyAssignment(property) ? property.name : undefined;
}

function identifierIs(expression: ts.Expression | undefined, name: string): boolean {
  return Boolean(expression && ts.isIdentifier(expression) && expression.text === name);
}

describe('IPC authority wiring', () => {
  it('registers every request channel exactly once with owned-renderer authority before effects', () => {
    const text = readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8');
    const source = ts.createSourceFile('index.ts', text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
    const registerIpc = source.statements.find((statement): statement is ts.FunctionDeclaration => ts.isFunctionDeclaration(statement)
      && statement.name?.text === 'registerIpc');
    expect(registerIpc?.body, 'the production IPC registration path exists').toBeDefined();

    const registrations = directRegistrations(registerIpc!.body!);
    const registrarCalls: ts.CallExpression[] = [];
    const findRegistrarCalls = (node: ts.Node): void => {
      if (ts.isCallExpression(node) && ts.isIdentifier(node.expression)
        && node.expression.text === 'registerDesktopClipboardHandlers') registrarCalls.push(node);
      ts.forEachChild(node, findRegistrarCalls);
    };
    findRegistrarCalls(registerIpc!.body!);

    expect(registrarCalls, 'clipboard request channels delegate to the executable registrar exactly once').toHaveLength(1);
    expect(registrarCalls[0].arguments).toHaveLength(1);
    const options = ts.isObjectLiteralExpression(registrarCalls[0].arguments[0]) ? registrarCalls[0].arguments[0] : undefined;
    expect(options, 'the clipboard registrar receives explicit production wiring').toBeDefined();
    expect(identifierIs(propertyExpression(options, 'channels'), 'IPC'), 'the registrar owns both IPC clipboard channels').toBe(true);
    const registrarIsGuarded = identifierIs(propertyExpression(options, 'authorize'), 'assertRenderer');
    expect(registrarIsGuarded, 'the registrar uses the production owned-renderer authority').toBe(true);

    for (const name of delegatedClipboardChannels) {
      const current = registrations.get(name) ?? [];
      current.push({ api: 'handle', guarded: registrarIsGuarded });
      registrations.set(name, current);
    }

    for (const [name, channel] of Object.entries(IPC)) {
      if ([IPC.output, IPC.exit, IPC.lifecycle, IPC.authLoginEvent].includes(channel)) continue;
      const channelRegistrations = registrations.get(name) ?? [];
      expect(channelRegistrations, `${channel} has exactly one production registration`).toHaveLength(1);
      expect(channelRegistrations[0].guarded, `${channel} guards before effects`).toBe(true);
      expect(channelRegistrations[0].api, `${channel} uses the expected registration API`).toBe(notifications.has(channel) ? 'on' : 'handle');
    }
  });
});

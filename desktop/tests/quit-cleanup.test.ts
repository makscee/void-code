import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { describe, expect, it, vi } from 'vitest';

type QuitCleanupActions = {
  teardownSessions(): void;
  cleanupClipboardImages(): void;
  cleanupProbe(): void;
};
type QuitCleanupModule = {
  runQuitCleanup?(actions: QuitCleanupActions): void;
};

async function quitCleanupModule(): Promise<QuitCleanupModule> {
  return import(new URL('../src/main/quit-cleanup.ts', import.meta.url).href) as Promise<QuitCleanupModule>;
}

async function runQuitCleanup(actions: QuitCleanupActions): Promise<void> {
  const module = await quitCleanupModule();
  expect(module.runQuitCleanup, 'quit cleanup seam is absent').toBeTypeOf('function');
  module.runQuitCleanup!(actions);
}

// The ordinary lifecycle is the baseline: each owned resource is released once, in dependency
// order. The failure cases below prove this order does not become an all-or-nothing chain.
describe('quit cleanup isolates owned-resource failures', () => {
  it('keeps the normal teardown, clipboard, and probe cleanup behavior exactly once', async () => {
    const calls: string[] = [];

    await expect(runQuitCleanup({
      teardownSessions: () => { calls.push('sessions'); },
      cleanupClipboardImages: () => { calls.push('clipboard'); },
      cleanupProbe: () => { calls.push('probe'); },
    })).resolves.toBeUndefined();

    expect(calls).toEqual(['sessions', 'clipboard', 'probe']);
  });

  // Without independent cleanup boundaries, a session-manager error leaves sensitive PNG files.
  it('cleans clipboard images exactly once after session teardown throws', async () => {
    const calls: string[] = [];
    const cleanupClipboardImages = vi.fn(() => { calls.push('clipboard'); });

    await expect(runQuitCleanup({
      teardownSessions: () => { calls.push('sessions'); throw new Error('PTY teardown failed'); },
      cleanupClipboardImages,
      cleanupProbe: () => { calls.push('probe'); },
    })).resolves.toBeUndefined();

    expect(cleanupClipboardImages).toHaveBeenCalledOnce();
    expect(calls).toEqual(['sessions', 'clipboard', 'probe']);
  });

  // Without independent cleanup boundaries, a filesystem cleanup error leaks the probe directory.
  it('runs probe cleanup after clipboard cleanup throws', async () => {
    const calls: string[] = [];
    const cleanupProbe = vi.fn(() => { calls.push('probe'); });

    await expect(runQuitCleanup({
      teardownSessions: () => { calls.push('sessions'); },
      cleanupClipboardImages: () => { calls.push('clipboard'); throw new Error('clipboard directory is locked'); },
      cleanupProbe,
    })).resolves.toBeUndefined();

    expect(cleanupProbe).toHaveBeenCalledOnce();
    expect(calls).toEqual(['sessions', 'clipboard', 'probe']);
  });

  // A quit handler has no recovery caller: every cleanup failure must be contained after all
  // cleanup attempts have been made, rather than changing Electron's quit path into an exception.
  it('contains failures from every cleanup step without aborting the quit handler', async () => {
    const calls: string[] = [];

    await expect(runQuitCleanup({
      teardownSessions: () => { calls.push('sessions'); throw new Error('session failure'); },
      cleanupClipboardImages: () => { calls.push('clipboard'); throw new Error('clipboard failure'); },
      cleanupProbe: () => { calls.push('probe'); throw new Error('probe failure'); },
    })).resolves.toBeUndefined();

    expect(calls).toEqual(['sessions', 'clipboard', 'probe']);
  });
});

function beforeQuitListener(source: ts.SourceFile): ts.ArrowFunction | ts.FunctionExpression {
  const registrations: ts.CallExpression[] = [];
  const visit = (node: ts.Node): void => {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression)
      && node.expression.expression.getText(source) === 'app' && node.expression.name.text === 'on'
      && ts.isStringLiteral(node.arguments[0]) && node.arguments[0].text === 'before-quit') registrations.push(node);
    ts.forEachChild(node, visit);
  };
  visit(source);

  expect(registrations, 'main must register one before-quit listener').toHaveLength(1);
  const listener = registrations[0].arguments[1];
  expect(listener, 'before-quit must provide a callback').toBeDefined();
  expect(ts.isArrowFunction(listener!) || ts.isFunctionExpression(listener!), 'before-quit callback must be a function').toBe(true);
  return listener! as ts.ArrowFunction | ts.FunctionExpression;
}

// index.ts cannot be imported without starting Electron. This intentionally checks only the one
// integration boundary: quit handling must delegate to the behaviorally tested seam, not rebuild
// its cleanup chain inline.
describe('production before-quit wiring', () => {
  it('delegates directly to the quit cleanup seam', () => {
    const source = ts.createSourceFile(
      'index.ts',
      readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8'),
      ts.ScriptTarget.Latest,
      true,
      ts.ScriptKind.TS,
    );
    const listener = beforeQuitListener(source);
    const statements = ts.isBlock(listener.body) ? listener.body.statements : [ts.factory.createExpressionStatement(listener.body)];

    expect(statements, 'before-quit must only delegate; cleanup ownership lives in runQuitCleanup').toHaveLength(1);
    const statement = statements[0];
    expect(ts.isExpressionStatement(statement) && ts.isCallExpression(statement.expression)).toBe(true);
    const call = (statement as ts.ExpressionStatement).expression as ts.CallExpression;
    expect(ts.isIdentifier(call.expression) && call.expression.text === 'runQuitCleanup').toBe(true);
  });
});

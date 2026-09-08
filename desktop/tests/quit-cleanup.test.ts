// rails:pin-on-coverage moving cleaned=true after runQuitCleanup lets synchronous teardown re-entry run all three cleanup actions twice; exact calls and action depth kill that mutation
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { describe, expect, it, vi } from 'vitest';

type QuitCleanupActions = {
  teardownSessions(): void;
  cleanupClipboardImages(): void;
  cleanupProbe(): void;
};
type OwnedCleanupCoordinator = {
  cleanup(): void;
};
type QuitCleanupModule = {
  runQuitCleanup?(actions: QuitCleanupActions): void;
  createOwnedCleanupCoordinator?(actions: QuitCleanupActions): OwnedCleanupCoordinator;
};

async function quitCleanupModule(): Promise<QuitCleanupModule> {
  return import(new URL('../src/main/quit-cleanup.ts', import.meta.url).href) as Promise<QuitCleanupModule>;
}

async function runQuitCleanup(actions: QuitCleanupActions): Promise<void> {
  const module = await quitCleanupModule();
  expect(module.runQuitCleanup, 'quit cleanup seam is absent').toBeTypeOf('function');
  module.runQuitCleanup!(actions);
}

async function ownedCleanupCoordinator(actions: QuitCleanupActions): Promise<OwnedCleanupCoordinator> {
  const module = await quitCleanupModule();
  expect(module.createOwnedCleanupCoordinator, 'idempotent owned-cleanup coordinator seam is absent').toBeTypeOf('function');
  return module.createOwnedCleanupCoordinator!(actions);
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

// app.exit bypasses before-quit. The coordinator is the behavioral ownership boundary shared by
// every exit origin below, so a normal quit followed by a session-end notification cannot double
// tear down a PTY or delete a directory twice.
describe('owned cleanup coordinator', () => {
  it('runs each owned teardown once when before-quit is followed by session-end and both app.exit paths', async () => {
    const calls: string[] = [];
    const coordinator = await ownedCleanupCoordinator({
      teardownSessions: () => { calls.push('sessions'); },
      cleanupClipboardImages: () => { calls.push('clipboard'); },
      cleanupProbe: () => { calls.push('probe'); },
    });
    const beforeQuit = (): void => coordinator.cleanup();
    const browserWindowSessionEnd = (): void => coordinator.cleanup();
    const headlessProbeExit = (): void => coordinator.cleanup();
    const failStartupExit = (): void => coordinator.cleanup();

    beforeQuit();
    browserWindowSessionEnd();
    headlessProbeExit();
    failStartupExit();

    expect(calls).toEqual(['sessions', 'clipboard', 'probe']);
  });

  // Marking ownership after actions lets synchronous teardown re-entry start a second cleanup pass.
  it('does not recurse into cleanup actions when the first action re-enters the same coordinator', async () => {
    const calls: string[] = [];
    let activeTeardowns = 0;
    let maximumActiveTeardowns = 0;
    let reentered = false;
    const coordinator: OwnedCleanupCoordinator = await ownedCleanupCoordinator({
      teardownSessions: () => {
        activeTeardowns += 1;
        maximumActiveTeardowns = Math.max(maximumActiveTeardowns, activeTeardowns);
        calls.push('sessions');
        if (!reentered) {
          reentered = true;
          coordinator.cleanup();
        }
        activeTeardowns -= 1;
      },
      cleanupClipboardImages: () => { calls.push('clipboard'); },
      cleanupProbe: () => { calls.push('probe'); },
    });

    coordinator.cleanup();

    expect(calls).toEqual(['sessions', 'clipboard', 'probe']);
    expect(maximumActiveTeardowns).toBe(1);
  });
});

function sourceFile(): ts.SourceFile {
  return ts.createSourceFile(
    'index.ts',
    readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8'),
    ts.ScriptTarget.Latest,
    true,
    ts.ScriptKind.TS,
  );
}

function eventListeners(source: ts.SourceFile, receiver: string, event: string): Array<ts.ArrowFunction | ts.FunctionExpression> {
  const listeners: Array<ts.ArrowFunction | ts.FunctionExpression> = [];
  const visit = (node: ts.Node): void => {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression)
      && node.expression.expression.getText(source) === receiver && node.expression.name.text === 'on'
      && ts.isStringLiteral(node.arguments[0]) && node.arguments[0].text === event) {
      const listener = node.arguments[1];
      if (listener && (ts.isArrowFunction(listener) || ts.isFunctionExpression(listener))) listeners.push(listener);
    }
    ts.forEachChild(node, visit);
  };
  visit(source);
  return listeners;
}

function ownedCleanupCoordinatorName(source: ts.SourceFile): string | undefined {
  const creations: ts.VariableDeclaration[] = [];
  const visit = (node: ts.Node): void => {
    if (ts.isVariableDeclaration(node) && node.initializer && ts.isCallExpression(node.initializer)
      && ts.isIdentifier(node.initializer.expression) && node.initializer.expression.text === 'createOwnedCleanupCoordinator') creations.push(node);
    ts.forEachChild(node, visit);
  };
  visit(source);

  expect(creations, 'main must construct one idempotent owned-cleanup coordinator').toHaveLength(1);
  const binding = creations[0]?.name;
  expect(binding && ts.isIdentifier(binding), 'the coordinator must have one identifier binding for all lifecycle origins').toBe(true);
  return binding && ts.isIdentifier(binding) ? binding.text : undefined;
}

function ownedCleanupCalls(root: ts.Node, source: ts.SourceFile, coordinator: string): ts.CallExpression[] {
  const calls: ts.CallExpression[] = [];
  const visit = (node: ts.Node): void => {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression)
      && node.expression.expression.getText(source) === coordinator && node.expression.name.text === 'cleanup') calls.push(node);
    ts.forEachChild(node, visit);
  };
  visit(root);
  return calls;
}

function assertOnlyOwnedCleanupDelegate(listener: ts.ArrowFunction | ts.FunctionExpression | undefined, source: ts.SourceFile, coordinator: string, lifecycle: string): void {
  expect(listener, `${lifecycle} must provide a callback`).toBeDefined();
  if (!listener) return;
  const statements = ts.isBlock(listener.body) ? listener.body.statements : [ts.factory.createExpressionStatement(listener.body)];
  expect(statements, `${lifecycle} must only delegate to the idempotent cleanup coordinator`).toHaveLength(1);
  expect(ownedCleanupCalls(listener, source, coordinator), `${lifecycle} must invoke the shared cleanup coordinator`).toHaveLength(1);
}

function callsAppExit(root: ts.Node, source: ts.SourceFile): ts.CallExpression[] {
  const calls: ts.CallExpression[] = [];
  const visit = (node: ts.Node): void => {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression)
      && node.expression.expression.getText(source) === 'app' && node.expression.name.text === 'exit') calls.push(node);
    ts.forEachChild(node, visit);
  };
  visit(root);
  return calls;
}

function namedFunction(source: ts.SourceFile, name: string): ts.FunctionDeclaration | undefined {
  let found: ts.FunctionDeclaration | undefined;
  const visit = (node: ts.Node): void => {
    if (ts.isFunctionDeclaration(node) && node.name?.text === name) found = node;
    ts.forEachChild(node, visit);
  };
  visit(source);
  return found;
}

function enclosingFunction(node: ts.Node): ts.Node | undefined {
  let current = node.parent;
  while (current) {
    if (ts.isArrowFunction(current) || ts.isFunctionExpression(current) || ts.isFunctionDeclaration(current)) return current;
    current = current.parent;
  }
  return undefined;
}

function assertCleanupPrecedesExit(scope: ts.Node | undefined, exit: ts.CallExpression | undefined, source: ts.SourceFile, exitPath: string): void {
  expect(scope, `${exitPath} must have a cleanup-owning callback`).toBeDefined();
  expect(exit, `${exitPath} must call app.exit`).toBeDefined();
  if (!scope || !exit) return;
  const cleanups: ts.CallExpression[] = [];
  const visit = (node: ts.Node): void => {
    if (ts.isCallExpression(node) && ts.isPropertyAccessExpression(node.expression) && node.expression.name.text === 'cleanup') cleanups.push(node);
    ts.forEachChild(node, visit);
  };
  visit(scope);
  expect(cleanups, `${exitPath} must invoke cleanup because app.exit bypasses before-quit`).toHaveLength(1);
  if (cleanups.length !== 1) return;
  expect(cleanups[0].getStart(source), `${exitPath} must clean before app.exit`).toBeLessThan(exit.getStart(source));
  const coordinator = ownedCleanupCoordinatorName(source);
  if (!coordinator) return;
  expect(ownedCleanupCalls(scope, source, coordinator), `${exitPath} must use the same coordinator as every other lifecycle origin`).toEqual(cleanups);
}

// index.ts cannot be imported without starting Electron. These deliberately narrow AST checks are
// backed by the coordinator's executable idempotency contract above; they only bind each Electron
// lifecycle origin to that one behavior rather than reimplementing cleanup in source-text tests.
describe('production owned cleanup wiring', () => {
  it('constructs one coordinator to own every cleanup origin', () => {
    ownedCleanupCoordinatorName(sourceFile());
  });

  it('delegates before-quit only to the shared coordinator', () => {
    const source = sourceFile();
    const beforeQuit = eventListeners(source, 'app', 'before-quit');
    expect(beforeQuit, 'main must register one before-quit listener').toHaveLength(1);
    const coordinator = ownedCleanupCoordinatorName(source);
    if (!coordinator) return;
    assertOnlyOwnedCleanupDelegate(beforeQuit[0], source, coordinator, 'before-quit');
  });

  it('delegates BrowserWindow session-end only to the shared coordinator', () => {
    const source = sourceFile();
    const sessionEnd = eventListeners(source, 'window', 'session-end');
    expect(sessionEnd, 'every BrowserWindow must register one Windows session-end listener').toHaveLength(1);
    const coordinator = ownedCleanupCoordinatorName(source);
    if (!coordinator) return;
    assertOnlyOwnedCleanupDelegate(sessionEnd[0], source, coordinator, 'BrowserWindow session-end');
  });

  it('cleans before headless-probe app.exit', () => {
    const source = sourceFile();
    const createWindow = namedFunction(source, 'createWindow');
    expect(createWindow, 'main must retain the headless-probe window factory').toBeDefined();
    if (!createWindow) return;
    const headlessExits = callsAppExit(createWindow, source);
    expect(headlessExits, 'headless probe must exit once after reporting its result').toHaveLength(1);
    assertCleanupPrecedesExit(enclosingFunction(headlessExits[0]), headlessExits[0], source, 'headless probe app.exit');
  });

  it('cleans before failStartup app.exit', () => {
    const source = sourceFile();
    const failStartup = namedFunction(source, 'failStartup');
    expect(failStartup, 'main must retain failStartup').toBeDefined();
    if (!failStartup) return;
    const startupExits = callsAppExit(failStartup, source);
    expect(startupExits, 'failStartup must exit once after reporting its failure').toHaveLength(1);
    assertCleanupPrecedesExit(failStartup, startupExits[0], source, 'failStartup app.exit');
  });
});

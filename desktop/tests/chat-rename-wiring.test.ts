// rails:pin-on-coverage post-implementation mutation strengthening: the feature already existed, and these seam checks were added because replacing either rename dispatch with select survived the focused suite; the executable preload check also distinguishes workspace:rename from workspace:select
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { IPC } from '../src/shared/preload-contract';

const electron = vi.hoisted(() => ({
  expose: vi.fn(),
  invoke: vi.fn(),
  on: vi.fn(),
  removeListener: vi.fn(),
  send: vi.fn(),
  sendSync: vi.fn(() => ({ ok: true })),
}));

vi.mock('electron', () => ({
  contextBridge: { exposeInMainWorld: electron.expose },
  ipcRenderer: {
    invoke: electron.invoke,
    on: electron.on,
    removeListener: electron.removeListener,
    send: electron.send,
    sendSync: electron.sendSync,
  },
}));

function source(relativePath: string): ts.SourceFile {
  const text = readFileSync(new URL(relativePath, import.meta.url), 'utf8');
  return ts.createSourceFile(relativePath, text, ts.ScriptTarget.Latest, true, ts.ScriptKind.TS);
}

function propertyPath(node: ts.Expression): string | undefined {
  if (ts.isIdentifier(node)) return node.text;
  if (ts.isPropertyAccessExpression(node)) {
    const owner = propertyPath(node.expression);
    return owner ? `${owner}.${node.name.text}` : undefined;
  }
  return undefined;
}

function callsWithin(node: ts.Node, path: string): ts.CallExpression[] {
  const calls: ts.CallExpression[] = [];
  function visit(child: ts.Node): void {
    if (ts.isCallExpression(child) && propertyPath(child.expression) === path) calls.push(child);
    ts.forEachChild(child, visit);
  }
  visit(node);
  return calls;
}

function argumentPaths(call: ts.CallExpression): Array<string | undefined> {
  return call.arguments.map((argument) => propertyPath(argument));
}

function workspaceRenameHandler(file: ts.SourceFile): ts.ArrowFunction | ts.FunctionExpression | undefined {
  let found: ts.ArrowFunction | ts.FunctionExpression | undefined;
  function visit(node: ts.Node): void {
    if (ts.isCallExpression(node) && propertyPath(node.expression) === 'ipcMain.handle'
      && propertyPath(node.arguments[0] as ts.Expression) === 'IPC.workspaceRename') {
      const handler = node.arguments[1];
      if (handler && (ts.isArrowFunction(handler) || ts.isFunctionExpression(handler))) found = handler;
    }
    ts.forEachChild(node, visit);
  }
  visit(file);
  return found;
}

function namedFunction(file: ts.SourceFile, name: string): ts.FunctionDeclaration | undefined {
  return file.statements.find((statement): statement is ts.FunctionDeclaration =>
    ts.isFunctionDeclaration(statement) && statement.name?.text === name);
}

// Importing main/index.ts starts Electron lifecycle work, while renderer/index.ts immediately
// dereferences its full DOM and starts workspace loading. Parsing only the named IPC handler and
// named renderer adapter is therefore a seam contract, not broad source mirroring: it asserts the
// operation and payload crossing each otherwise non-importable module boundary. Store behaviour
// and the pure rename reducer remain executable in workspace-rename and chat-tab-rename tests.
describe('chat rename wiring seams', () => {
  it('main dispatches the validated rename request to WorkspaceStore.rename', () => {
    const handler = workspaceRenameHandler(source('../src/main/index.ts'));
    expect(handler, 'IPC.workspaceRename handler is missing').toBeDefined();
    const calls = callsWithin(handler!, 'workspace.rename');
    expect(calls).toHaveLength(1);
    expect(argumentPaths(calls[0])).toEqual(['request.sessionId', 'request.title']);
  });

  it('renderer dispatches a rename effect through the rename bridge with id and title', () => {
    const renameChat = namedFunction(source('../src/renderer/index.ts'), 'renameChat');
    expect(renameChat, 'renameChat adapter is missing').toBeDefined();
    const calls = callsWithin(renameChat!, 'window.voidTerminal.workspace.rename');
    expect(calls).toHaveLength(1);
    expect(argumentPaths(calls[0])).toEqual(['sessionId', 'title']);
  });
});

describe('chat rename preload bridge', () => {
  beforeEach(async () => {
    vi.resetModules();
    vi.clearAllMocks();
    vi.stubGlobal('window', { addEventListener: vi.fn() });
    await import('../src/preload/index');
  });

  afterEach(() => vi.unstubAllGlobals());

  it('invokes the distinct rename channel with both chat id and title', async () => {
    const api = electron.expose.mock.calls[0]?.[1] as { workspace: { rename(id: string, title: string): Promise<unknown> } };
    electron.invoke.mockResolvedValueOnce({ workspace: null });

    await api.workspace.rename('chat-id', 'Quarterly close');

    expect(electron.invoke).toHaveBeenCalledOnce();
    expect(electron.invoke).toHaveBeenCalledWith(IPC.workspaceRename, { sessionId: 'chat-id', title: 'Quarterly close' });
    expect(IPC.workspaceRename).not.toBe(IPC.workspaceSelect);
  });
});

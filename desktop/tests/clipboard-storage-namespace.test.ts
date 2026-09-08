// rails:pin-on-coverage clipboardStorageRoot already normalized userData, but the old path.join-built equivalent normalized before reaching production; a literal unused/.. spelling now kills hashing raw userData
import { existsSync, mkdtempSync, rmSync, utimesSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { readFileSync } from 'node:fs';
import ts from 'typescript';
import { afterEach, describe, expect, it } from 'vitest';

type ClipboardImageStorage = {
  directory: string;
  writeImage(png: Buffer): string;
  cleanup(): void;
};

type ClipboardImageStorageOptions = {
  temporaryDirectory(): string;
  uniqueId(): string;
  processId: number;
  now(): number;
};

type ClipboardStorageModule = {
  clipboardStorageRoot?: (temporaryDirectory: string, userData: string) => string;
  createClipboardImageStorage?: (options: ClipboardImageStorageOptions) => ClipboardImageStorage;
};

const roots: string[] = [];
afterEach(() => {
  for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});

function temporaryRoot(): string {
  const root = mkdtempSync(path.join(os.tmpdir(), 'void-code-clipboard-namespace-test-'));
  roots.push(root);
  return root;
}

function deterministicUuid(sequence: number): string {
  return `00000000-0000-4000-8000-${sequence.toString().padStart(12, '0')}`;
}

function storageOptions(root: string, processId: number, id: string, now: number): ClipboardImageStorageOptions {
  return { temporaryDirectory: () => root, uniqueId: () => id, processId, now: () => now };
}

async function clipboardStorage(): Promise<ClipboardStorageModule> {
  return import(new URL('../src/main/clipboard-paste.ts', import.meta.url).href) as Promise<ClipboardStorageModule>;
}

function namespaceRoot(module: ClipboardStorageModule, temporaryDirectory: string, userData: string): string {
  expect(module.clipboardStorageRoot, 'the pure userData-scoped clipboard storage-root seam is absent').toBeTypeOf('function');
  return module.clipboardStorageRoot!(temporaryDirectory, userData);
}

function imageStorage(module: ClipboardStorageModule): NonNullable<ClipboardStorageModule['createClipboardImageStorage']> {
  expect(module.createClipboardImageStorage, 'process-owned clipboard image storage is absent').toBeTypeOf('function');
  return module.createClipboardImageStorage!;
}

function objectLiteral(expression: ts.Expression | undefined): ts.ObjectLiteralExpression | undefined {
  return expression && ts.isObjectLiteralExpression(expression) ? expression : undefined;
}

function objectProperty(object: ts.ObjectLiteralExpression | undefined, name: string): ts.Expression | undefined {
  const property = object?.properties.find((candidate) => ts.isPropertyAssignment(candidate)
    && (ts.isIdentifier(candidate.name) || ts.isStringLiteral(candidate.name)) && candidate.name.text === name);
  return property && ts.isPropertyAssignment(property) ? property.initializer : undefined;
}

function callsNamed(root: ts.Node, name: string): ts.CallExpression[] {
  const calls: ts.CallExpression[] = [];
  const visit = (node: ts.Node): void => {
    if (ts.isCallExpression(node) && ts.isIdentifier(node.expression) && node.expression.text === name) calls.push(node);
    ts.forEachChild(node, visit);
  };
  visit(root);
  return calls;
}

function isSingleInstanceLock(statement: ts.Statement): statement is ts.IfStatement {
  return ts.isIfStatement(statement) && ts.isPrefixUnaryExpression(statement.expression)
    && statement.expression.operator === ts.SyntaxKind.ExclamationToken
    && ts.isCallExpression(statement.expression.operand)
    && ts.isPropertyAccessExpression(statement.expression.operand.expression)
    && statement.expression.operand.expression.expression.getText() === 'app'
    && statement.expression.operand.expression.name.text === 'requestSingleInstanceLock';
}

// Without this seam, a production probe's userData override and the normal desktop share one prune domain.
describe('clipboard storage root is scoped to Electron userData', () => {
  it('maps equivalent normalized userData paths to one stable absolute child of temp', async () => {
    const module = await clipboardStorage();
    const sandbox = temporaryRoot();
    const temporaryDirectory = path.join(sandbox, 'temp');
    const userData = path.join(sandbox, 'profiles', 'primary');
    const equivalentUserData = `${sandbox}${path.sep}profiles${path.sep}unused${path.sep}..${path.sep}primary`;
    expect(equivalentUserData).toContain(`${path.sep}unused${path.sep}..${path.sep}`);

    const first = namespaceRoot(module, temporaryDirectory, userData);
    const second = namespaceRoot(module, temporaryDirectory, equivalentUserData);

    expect(first).toBe(second);
    expect(path.isAbsolute(first)).toBe(true);
    expect(path.dirname(first)).toBe(path.resolve(temporaryDirectory));
  });

  it('gives different userData paths different opaque roots without leaking either raw path into a basename', async () => {
    const module = await clipboardStorage();
    const sandbox = temporaryRoot();
    const temporaryDirectory = path.join(sandbox, 'temp');
    const firstUserData = path.join(sandbox, 'profiles', 'customer-A-private-userData');
    const secondUserData = path.join(sandbox, 'profiles', 'customer-B-private-userData');

    const first = namespaceRoot(module, temporaryDirectory, firstUserData);
    const second = namespaceRoot(module, temporaryDirectory, secondUserData);

    expect(first).not.toBe(second);
    for (const [root, userData] of [[first, firstUserData], [second, secondUserData]] as const) {
      expect(path.isAbsolute(root)).toBe(true);
      expect(path.dirname(root)).toBe(path.resolve(temporaryDirectory));
      expect(path.relative(path.resolve(temporaryDirectory), root)).not.toMatch(/[\\/]/);
      expect(path.basename(root)).not.toContain(path.resolve(userData));
      expect(path.basename(root)).not.toContain(path.basename(userData));
    }
  });

  it('cannot let namespace B prune exact stale and live-looking owned directories in namespace A', async () => {
    const module = await clipboardStorage();
    const create = imageStorage(module);
    const sandbox = temporaryRoot();
    const temporaryDirectory = path.join(sandbox, 'temp');
    const namespaceA = namespaceRoot(module, temporaryDirectory, path.join(sandbox, 'profiles', 'A'));
    const namespaceB = namespaceRoot(module, temporaryDirectory, path.join(sandbox, 'profiles', 'B'));
    const now = Date.UTC(2026, 8, 8, 12);
    const stale = now - 8 * 24 * 60 * 60 * 1000;

    const staleA = create(storageOptions(namespaceA, 5101, deterministicUuid(1), stale));
    const liveLookingA = create(storageOptions(namespaceA, process.pid, deterministicUuid(2), stale));
    const staleB = create(storageOptions(namespaceB, 5201, deterministicUuid(3), stale));
    utimesSync(staleA.directory, stale / 1000, stale / 1000);
    utimesSync(liveLookingA.directory, stale / 1000, stale / 1000);
    utimesSync(staleB.directory, stale / 1000, stale / 1000);

    const currentB = create(storageOptions(namespaceB, 5299, deterministicUuid(4), now));

    expect(existsSync(staleB.directory), 'namespace B prunes its own exact stale directory').toBe(false);
    expect(existsSync(staleA.directory), 'namespace B must not prune namespace A exact stale directory').toBe(true);
    expect(existsSync(liveLookingA.directory), 'namespace B must not prune namespace A exact directory named with a live PID').toBe(true);
    expect(existsSync(currentB.directory), 'namespace B creates its current owned directory').toBe(true);
  });
});

// The Electron module has startup side effects, so this checks the executable AST rather than importing main.
describe('clipboard storage namespace production wiring', () => {
  it('creates storage after winning the lock from the current app userData, never bare os.tmpdir', () => {
    const source = ts.createSourceFile(
      'index.ts',
      readFileSync(new URL('../src/main/index.ts', import.meta.url), 'utf8'),
      ts.ScriptTarget.Latest,
      true,
      ts.ScriptKind.TS,
    );
    const lockBranch = source.statements.find(isSingleInstanceLock);
    expect(lockBranch?.elseStatement, 'main must initialize the winning instance inside the single-instance-lock else branch').toBeDefined();
    if (!lockBranch?.elseStatement) return;

    const safeCreates = callsNamed(lockBranch.elseStatement, 'createSafeClipboardImageStorage');
    expect(safeCreates, 'winning instance must create optional clipboard storage exactly once').toHaveLength(1);
    const primaryCreates = callsNamed(safeCreates[0].arguments[1], 'createPrimaryClipboardImageStorage');
    expect(primaryCreates, 'safe storage must defer exactly one primary storage constructor').toHaveLength(1);
    const options = objectLiteral(primaryCreates[0].arguments[0]);
    const temporaryDirectory = objectProperty(options, 'temporaryDirectory');
    expect(temporaryDirectory, 'primary storage must receive a scoped temporary-directory factory').toBeDefined();
    expect(ts.isArrowFunction(temporaryDirectory), 'storage root must be computed only after the winning lock').toBe(true);
    if (!temporaryDirectory || !ts.isArrowFunction(temporaryDirectory)) return;

    const rootCalls = callsNamed(temporaryDirectory.body, 'clipboardStorageRoot');
    expect(rootCalls, 'storage must derive its root through the userData namespace seam, not pass os.tmpdir directly').toHaveLength(1);
    expect(rootCalls[0].arguments.map((argument) => argument.getText(source))).toEqual([
      'os.tmpdir()',
      "app.getPath('userData')",
    ]);
  });
});

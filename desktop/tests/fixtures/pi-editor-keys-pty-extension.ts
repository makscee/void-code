// Loaded by actual Pi CLI/SDK loader inside an owned PTY. No fake renderer/receiver.
// The runner copies CURRENT raw Go source to managed.ts in its owned temporary dir.
import assert from 'node:assert/strict';
import { appendFileSync } from 'node:fs';
import childProcess from 'node:child_process';
import { syncBuiltinESMExports } from 'node:module';
import { CustomEditor, type ExtensionAPI } from '@earendil-works/pi-coding-agent';
import managed from './managed.ts';

const draft = 'owned 😀\nmiddle 世界\nlast';
export default async function (pi: ExtensionAPI) {
  assert.equal(process.env.VC_EDITOR_KEYS_OWNED_PTY, '1');
  const log = (record: object) => appendFileSync(process.env.VC_EDITOR_KEYS_OBSERVER!, JSON.stringify(record) + '\n');
  // No external credentials, model discovery or inference; even accidental submit is handled.
  globalThis.fetch = (() => { log({ event: 'forbidden-network' }); throw new Error('offline fixture'); }) as typeof fetch;
  pi.registerProvider('editor-keys-offline', {
    baseUrl: 'http://127.0.0.1:1', apiKey: 'owned-fixture-only', api: 'openai-completions',
    models: [{ id: 'fixture', name: 'Offline fixture', reasoning: false, input: ['text'], cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 }, contextWindow: 4096, maxTokens: 128 }],
    streamSimple: () => { log({ event: 'forbidden-inference' }); throw new Error('input must be handled'); },
  });
  pi.on('project_trust', () => ({ trusted: 'yes', remember: false }));
  let handled = 0;
  pi.on('input', () => { log({ event: 'handled', count: ++handled }); return { action: 'handled' }; });
  if (process.env.VC_EDITOR_KEYS_BASELINE !== '1') {
    const original = childProcess.execFileSync;
    childProcess.execFileSync = ((file: string, args: string[]) => {
      assert.equal(file, process.execPath); assert.deepEqual(args, ['pi-bootstrap']);
      return JSON.stringify({ version: 1, relayUrl: 'http://127.0.0.1:1', authToken: 'owned', providers: [] });
    }) as typeof original;
    try { syncBuiltinESMExports(); await managed(pi); }
    finally { childProcess.execFileSync = original; syncBuiltinESMExports(); }
  }
  pi.on('session_start', (_event, ctx) => {
    assert.equal(ctx.mode, 'tui');
    let editor: CustomEditor | undefined;
    // Actual widget acquisition: retain only the focused editor object, not a raw TUI facade.
    ctx.ui.setWidget('editor-keys-metadata-observer', (tui) => {
      const focused = (tui as unknown as { focusedComponent: unknown }).focusedComponent;
      assert.ok(focused instanceof CustomEditor, 'actual SDK-exported CustomEditor required');
      editor = focused;
      return { render: () => [], invalidate() {} };
    });
    const stop = ctx.ui.onTerminalInput(() => {
      queueMicrotask(() => {
        assert.ok(editor);
        const text = editor.getExpandedText();
        log({ event: 'snapshot', empty: text === '', draft: text === draft, older: text === 'owned USER one', latest: text === 'owned USER two', cursor: editor.getCursor(), length: text.length });
      });
    });
    pi.on('session_shutdown', () => stop());
    log({ event: 'ready', editor: editor?.constructor.name, customFactory: !!ctx.ui.getEditorComponent() });
  });
}

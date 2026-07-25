const sessionId = 'packaged-fixture-smoke';
const output: string[] = [];
let exited = false;
let sentHello = false;
let sentQuit = false;
const finish = (ok: boolean, error?: string): void => {
  document.title = `VOID_SMOKE:${JSON.stringify({ ok, output: output.join(''), exited, rendererAuthority: { process: typeof process, require: typeof require }, apiKeys: Object.keys(window.voidTerminal).sort(), error })}`;
};
(async () => {
  try {
    await window.voidTerminal.start({ sessionId, fixture: 'roundTrip' });
    const offOutput = window.voidTerminal.onOutput(sessionId, (event) => {
      output.push(event.data);
      if (!sentHello && output.join('').includes('fixture:ready')) { sentHello = true; void window.voidTerminal.input({ sessionId, data: 'hello-private-runtime\r' }); }
      if (!sentQuit && output.join('').includes('fixture:hello-private-runtime')) { sentQuit = true; void window.voidTerminal.input({ sessionId, data: 'quit\r' }); }
    });
    const offExit = window.voidTerminal.onExit(sessionId, () => {
      exited = true;
      offOutput(); offExit(); window.voidTerminal.teardown();
      finish(output.join('').includes('fixture:bye'));
    });
    await window.voidTerminal.resize({ sessionId, cols: 100, rows: 30 });
  } catch (error) { finish(false, error instanceof Error ? error.message : String(error)); }
})();

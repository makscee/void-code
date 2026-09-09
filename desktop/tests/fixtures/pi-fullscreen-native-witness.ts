import type { spawn } from 'node:child_process';

const phases = ['ASTRA_PHASE_STDIN_BEFORE', 'ASTRA_PHASE_STDIN_AFTER', 'ASTRA_PHASE_FORMS_BEFORE', 'ASTRA_PHASE_FORMS_AFTER', 'ASTRA_PHASE_SETTEXT_BEFORE', 'ASTRA_PHASE_SETTEXT_AFTER'] as const;

// Failure-only, owned private-station child. Never an acceptance retry.
export async function nativeWitness(originalSpawn: typeof spawn, args: Parameters<typeof spawn>, marker: string, emit: (record: object) => void): Promise<void> {
  const argv = [...args[1] as string[]];
  let script = argv[argv.length - 1];
  const insertions = [
    ['$text=$reader.ReadToEnd()', 0, 1],
    ['Add-Type -AssemblyName System.Windows.Forms', 2, 3],
    ['[Windows.Forms.Clipboard]::SetText($text)', 4, 5],
  ] as const;
  for (const [text, before, after] of insertions) {
    if (script.split(text).length !== 2) throw new Error('WITNESS_SCRIPT_REFUSED');
    script = script.replace(text, `[Console]::Error.WriteLine('${phases[before]}'); ${text}; [Console]::Error.WriteLine('${phases[after]}')`);
  }
  argv[argv.length - 1] = script;
  const started = performance.now();
  const log = (event: string, code: number | string | null = null, signal: NodeJS.Signals | null = null) => emit({ diagnostic: 'native-phase', event, elapsedMs: Math.round(performance.now() - started), code, signal });
  const errorCode = (error: NodeJS.ErrnoException) => typeof error.code === 'string' && /^[A-Z][A-Z0-9_]{0,63}$/.test(error.code) ? error.code : null;
  await new Promise<void>((resolve) => {
    let child: ReturnType<typeof spawn>;
    try { child = originalSpawn(args[0], argv, args[2]); }
    catch (error) { log('throw', errorCode(error as NodeJS.ErrnoException)); resolve(); return; }
    const terminate = () => {
      try { child.kill('SIGKILL'); }
      catch (error) { log('kill-error', errorCode(error as NodeJS.ErrnoException)); }
    };
    let done = false;
    let line = '';
    let discard = false;
    const seen = new Set<string>();
    const finish = () => {
      if (done) return;
      done = true;
      clearTimeout(kill); clearTimeout(bound);
      child.stdin?.destroy(); child.stdout?.destroy(); child.stderr?.destroy();
      resolve();
    };
    // Leave one second for close after kill; private launcher's job reaps all
    // descendants before emitting output even if Windows never delivers close.
    const kill = setTimeout(() => { log('kill'); terminate(); }, 4000);
    const bound = setTimeout(() => { log('close-bound'); terminate(); finish(); }, 5000);
    child.on('spawn', () => log('spawn'));
    child.on('error', (error) => log('error', errorCode(error)));
    child.stdin?.on('error', (error) => log('stdin-error', errorCode(error)));
    child.on('exit', (code, signal) => log('exit', code, signal));
    child.on('close', (code, signal) => { log('close', code, signal); finish(); });
    child.stderr?.on('data', (data: Buffer) => {
      // Exact complete lines only, bounded across chunks; never emit other bytes.
      for (const byte of data) {
        if (byte === 10) {
          const value = line.endsWith('\r') ? line.slice(0, -1) : line;
          if (!discard && phases.some((phase) => phase === value) && !seen.has(value)) { seen.add(value); log(value); }
          line = ''; discard = false;
        } else if (line.length < 64 && !discard) line += String.fromCharCode(byte);
        else discard = true;
      }
    });
    try {
      child.stdin?.setDefaultEncoding?.('utf8');
      child.stdin?.end(marker, 'utf8');
    } catch (error) { log('stdin-throw', errorCode(error as NodeJS.ErrnoException)); terminate(); }
  });
}

export async function preserveNativeFailure(error: unknown, diagnostic: () => Promise<void>): Promise<never> {
  try { await diagnostic(); } catch { /* Supplemental failure must not replace acceptance failure. */ }
  throw error;
}

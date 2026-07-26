import { spawn } from 'node:child_process';
import readline from 'node:readline';

process.title = 'void-code-owned-round-trip-fixture';
const input = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
const children = new Set<ReturnType<typeof spawn>>();
const closeChildren = (): void => { for (const child of children) child.kill(); };
process.on('exit', closeChildren);
setTimeout(() => console.log(`fixture:ready:cwd=${process.cwd()}`), 100);
input.on('line', (line) => {
  if (line === 'size') {
    if (process.platform === 'win32') {
      const powershell = `${process.env.SystemRoot ?? 'C:\\Windows'}\\System32\\WindowsPowerShell\\v1.0\\powershell.exe`;
      const query = spawn(powershell, ['-NoProfile', '-NonInteractive', '-Command', '$s=$Host.UI.RawUI.WindowSize; Write-Output "fixture:size:$($s.Width)x$($s.Height)"'], { stdio: 'inherit' });
      children.add(query);
      query.once('exit', () => children.delete(query));
    } else console.log(`fixture:size:${process.stdout.columns}x${process.stdout.rows}`);
    return;
  }
  if (line === 'tree') {
    const child = spawn(process.execPath, ['-e', 'setInterval(()=>{},1000)'], { stdio: 'ignore' });
    children.add(child);
    child.once('exit', () => children.delete(child));
    console.log(`fixture:pids:${process.pid}:${child.pid}`);
    return;
  }
  if (line === 'terminal-fidelity') {
    const ansi16 = [...Array(8).keys()].map((index) => `\x1b[${30 + index}mX`).join('') + [...Array(8).keys()].map((index) => `\x1b[${90 + index}mX`).join('');
    process.stdout.write(`\x1b[2J\x1b[H${ansi16}\x1b[0m\r\n`);
    process.stdout.write('\x1b[38;5;196mX\x1b[38;2;12;34;56mX\x1b[0mR\r\n');
    process.stdout.write('\x1b[1mX\x1b[2mX\x1b[3mX\x1b[4mX\x1b[7mX\x1b[9mX\x1b[0mR\r\n');
    process.stdout.write('XXXXX\x1b[2D\x1b[KYY\r\n');
    process.stdout.write(`${process.env.VOID_FIDELITY_PERTURB === 'box' ? '???' : '┌─┐'}e\u0301界\r\n`);
    setTimeout(() => input.close(), 2_000);
    return;
  }
  if (line === 'quit') {
    console.log('fixture:bye');
    closeChildren();
    input.close();
    return;
  }
  console.log(`fixture:${line}`);
});

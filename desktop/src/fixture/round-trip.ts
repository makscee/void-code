import readline from 'node:readline';

process.title = 'void-code-owned-round-trip-fixture';
const input = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
setTimeout(() => console.log('fixture:ready'), 100);
input.on('line', (line) => {
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
    input.close();
    return;
  }
  console.log(`fixture:${line}`);
});

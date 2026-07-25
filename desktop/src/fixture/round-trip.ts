import readline from 'node:readline';

process.title = 'void-code-owned-round-trip-fixture';
const input = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
setTimeout(() => console.log('fixture:ready'), 100);
input.on('line', (line) => {
  if (line === 'quit') {
    console.log('fixture:bye');
    input.close();
    return;
  }
  console.log(`fixture:${line}`);
});

// Offline regression of the complete managed provider, not a resolver mock or model listing.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { existsSync, mkdirSync, symlinkSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const [root, extension, work] = process.argv.slice(2);
const require = createRequire(path.join(root, 'package.json'));
const { createJiti } = await import(pathToFileURL(require.resolve('jiti')));
const aiModules = require.resolve.paths('@earendil-works/pi-ai').find(dir => existsSync(path.join(dir, '@earendil-works/pi-ai/package.json')));
assert.ok(aiModules, 'test requires the runtime pi-ai dependency');
const compatPath = path.join(aiModules, '@earendil-works/pi-ai/dist/compat.js');
const compat = await import(pathToFileURL(compatPath));
const aiRoot = path.dirname(path.dirname(compatPath));
const vendorRoot = path.join(work, 'bundled runtime #1');
mkdirSync(path.join(vendorRoot, 'vendor'), { recursive: true });
symlinkSync(path.dirname(compatPath), path.join(vendorRoot, 'vendor/pi-ai'), 'dir');
// Resolve from the runtime package, including hoisted dependencies and URL-sensitive paths.
const hoisted = path.join(work, 'hoisted runtime #2');
mkdirSync(path.join(hoisted, 'node_modules/@earendil-works'), { recursive: true });
symlinkSync(aiRoot, path.join(hoisted, 'node_modules/@earendil-works/pi-ai'), 'dir');
mkdirSync(path.join(hoisted, 'app'), { recursive: true });
const missing = path.join(work, 'missing runtime');
mkdirSync(missing);
// NODE_PATH is supplied at process start by the Go runner; a global fallback must not execute it.
assert.equal(process.env.NODE_PATH, path.join(work, 'global-modules'));
const poison = path.join(process.env.NODE_PATH, '@earendil-works/pi-ai');
mkdirSync(path.join(poison, 'dist/api'), { recursive: true });
writeFileSync(path.join(poison, 'package.json'), JSON.stringify({ name: '@earendil-works/pi-ai', type: 'module' }));
writeFileSync(path.join(poison, 'dist/api/openai-responses-shared.js'), 'throw new Error("UNRELATED_GLOBAL_PACKAGE_EXECUTED");');

let failed = 0;
for (const scenario of [
  { name: 'native-alias', root, alias: true },
  { name: 'virtual-native-no-env', root },
  { name: 'virtual-hoisted-no-env', root: path.join(hoisted, 'app') },
  { name: 'virtual-vendored', root: vendorRoot, packageDir: vendorRoot },
  { name: 'missing-native', root: missing, error: true },
  { name: 'missing-vendored', root: missing, packageDir: missing, error: true },
]) {
  delete process.env.PI_PACKAGE_DIR;
  if (scenario.packageDir) process.env.PI_PACKAGE_DIR = scenario.packageDir;
  const jiti = createJiti(import.meta.url, {
    moduleCache: false, fsCache: false, tryNative: false,
    ...(scenario.alias ? { alias: {
      '@earendil-works/pi-ai': compatPath,
      '@earendil-works/pi-ai/compat': compatPath,
      '@earendil-works/pi-coding-agent': path.join(root, 'dist/index.js'),
    } } : { virtualModules: {
      '@earendil-works/pi-ai': compat,
      '@earendil-works/pi-ai/compat': compat,
      '@earendil-works/pi-coding-agent': { getPackageDir: () => scenario.root },
    } }),
  });
  let requests = 0;
  globalThis.fetch = async (url, options) => {
    requests++;
    assert.equal(url, 'https://relay.invalid/codex/responses');
    const body = JSON.parse(options.body);
    assert.equal(body.model, 'gpt-5.6-terra');
    assert.equal(body.tools[0].name, 'read');
    assert.ok(body.input.length > 0);
    assert.equal(body.max_output_tokens, undefined);
    const item = { type: 'function_call', id: 'fc_probe', call_id: 'call_probe', name: 'read', arguments: '{"path":"fixture.md"}' };
    const events = [
      { type: 'response.created', response: { id: 'resp_probe' } },
      { type: 'response.output_item.added', output_index: 0, item: { ...item, arguments: '' } },
      { type: 'response.function_call_arguments.delta', output_index: 0, delta: item.arguments },
      { type: 'response.output_item.done', output_index: 0, item },
      { type: 'response.completed', response: { status: 'completed', usage: { input_tokens: 10, output_tokens: 2, total_tokens: 12 } } },
    ];
    return new Response(events.map(e => 'data: ' + JSON.stringify(e) + '\n\n').join(''), { headers: { 'content-type': 'text/event-stream' } });
  };
  try {
    const providers = new Map();
    const factory = await jiti.import(extension, { default: true });
    factory({ on() {}, registerProvider(id, config) { providers.set(id, config); } });
    const provider = providers.get('void-codex');
    assert.ok(provider, 'provider must register');
    const model = { ...provider.models[0], provider: 'void-codex', api: provider.api };
    const stream = provider.streamSimple(model, {
      systemPrompt: 'Offline regression',
      messages: [{ role: 'user', content: 'Read fixture.md', timestamp: 1 }],
      tools: [{ name: 'read', description: 'Read a fixture', parameters: { type: 'object', properties: { path: { type: 'string' } }, required: ['path'] } }],
    });
    const events = [];
    for await (const event of stream) events.push(event.type);
    const result = await stream.result();
    if (scenario.error) {
      assert.equal(result.stopReason, 'error');
      assert.match(result.errorMessage, /Responses helpers/);
      assert.doesNotMatch(result.errorMessage, /UNRELATED_GLOBAL_PACKAGE_EXECUTED/);
      assert.equal(requests, 0, 'missing helper must fail before transport');
    } else {
      assert.equal(result.stopReason, 'toolUse', result.errorMessage);
      assert.equal(requests, 1);
      assert.ok(events.includes('toolcall_end'));
      assert.deepEqual(result.content[0].arguments, { path: 'fixture.md' });
      assert.equal(result.content[0].name, 'read');
      assert.equal(result.usage.totalTokens, 12);
    }
    console.log(JSON.stringify({ scenario: scenario.name, pass: true, requests }));
  } catch (error) {
    failed++;
    console.error(JSON.stringify({ scenario: scenario.name, pass: false, error: error.message }));
  }
}
process.exitCode = failed ? 1 : 0;

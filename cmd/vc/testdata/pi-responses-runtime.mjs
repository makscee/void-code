// Offline regression of the complete managed provider, not a resolver mock or model listing.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { execFileSync } from 'node:child_process';
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
const { normalizeContext } = await import(pathToFileURL(path.join(aiRoot, 'dist/index.js')));
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
    // The real Pi loader supplies pi-tui in every environment, even without AI helpers.
    alias: {
      '@earendil-works/pi-tui': require.resolve('@earendil-works/pi-tui'),
      ...(scenario.alias ? {
        '@earendil-works/pi-ai': compatPath,
        '@earendil-works/pi-ai/compat': compatPath,
        '@earendil-works/pi-coding-agent': path.join(root, 'dist/index.js'),
      } : {}),
    },
    ...(scenario.alias ? {} : { virtualModules: {
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
    assert.equal(body.model, 'gpt-6-sol');
    assert.equal(body.instructions, 'You are the VC GPT-6 Sol coding assistant. Use bash to inspect this isolated fixture.');
    assert.equal(body.tools[0].name, 'bash');
    assert.ok(body.input.length > 0);
    assert.equal(body.max_output_tokens, undefined);
    const item = { type: 'function_call', id: 'fc_probe', call_id: 'call_probe', name: 'bash', arguments: '{"command":"printf vc-0871-safe"}' };
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
    // The installed 0.87.1 pi-ai adapter (as called by agent-core) strips
    // top-level shorthand before handing the transcript to this provider.
    const context = normalizeContext({
      systemPrompt: 'You are the VC GPT-6 Sol coding assistant. Use bash to inspect this isolated fixture.',
      messages: [{ role: 'user', content: 'Run the harmless printf probe', timestamp: 1 }],
      tools: [{ name: 'bash', description: 'Run a shell command', parameters: { type: 'object', properties: { command: { type: 'string' } }, required: ['command'] } }],
    });
    assert.equal(context.systemPrompt, undefined);
    assert.equal(context.tools, undefined);
    const stream = provider.streamSimple(model, context);
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
      assert.deepEqual(result.content[0].arguments, { command: 'printf vc-0871-safe' });
      assert.equal(result.content[0].name, 'bash');
      assert.equal(execFileSync('/bin/sh', ['-c', result.content[0].arguments.command], { cwd: work, encoding: 'utf8' }), 'vc-0871-safe');
      assert.equal(result.usage.totalTokens, 12);
    }
    console.log(JSON.stringify({ scenario: scenario.name, pass: true, requests }));
  } catch (error) {
    failed++;
    console.error(JSON.stringify({ scenario: scenario.name, pass: false, error: error.message }));
  }
}
// Billing refusals (void-board#373): Relay's 402 sentences, the unpaid week and the weekly limit
// alike, end the turn in the terminal UI as a plain reply, since Pi follows only an errored reply
// with its «/bug sends a report» line (maybeSuggestBugReport: stopReason !== "error" returns).
// Any other failure stays an error, and the model never sees a refusal as something it said.
try {
  const jiti = createJiti(import.meta.url, {
    moduleCache: false, fsCache: false, tryNative: false,
    alias: {
      '@earendil-works/pi-tui': require.resolve('@earendil-works/pi-tui'),
      '@earendil-works/pi-ai': compatPath,
      '@earendil-works/pi-ai/compat': compatPath,
      '@earendil-works/pi-coding-agent': path.join(root, 'dist/index.js'),
    },
  });
  const providers = new Map();
  const handlers = new Map();
  const factory = await jiti.import(extension, { default: true });
  factory({
    on(event, handler) { handlers.set(event, [...(handlers.get(event) ?? []), handler]); },
    registerProvider(id, config) { providers.set(id, config); },
  });
  const provider = providers.get('void-codex');
  const model = { ...provider.models[0], provider: 'void-codex', api: provider.api };
  const messageEnd = async (message, mode) => {
    let result;
    for (const handler of handlers.get('message_end') ?? []) result = (await handler({ type: 'message_end', message }, { mode, hasUI: true })) ?? result;
    return result?.message;
  };
  const context = normalizeContext({ systemPrompt: 'probe', messages: [{ role: 'user', content: 'Reply with exactly PONG', timestamp: 1 }] });
  const answer = async (status, body) => {
    globalThis.fetch = async () => new Response(typeof body === 'string' ? body : JSON.stringify(body), { status, headers: { 'content-type': typeof body === 'string' ? 'text/plain' : 'application/json' } });
    const stream = provider.streamSimple(model, context);
    for await (const _ of stream) {}
    return stream.result();
  };
  const unpaid = 'Баланса не хватает на эту неделю — пополнить: https://profile.makscee.ru/vc/pay';
  const limit = 'Лимит на эту неделю исчерпан. Он обновится 6 октября в 01:17 МСК. Сейчас можно перейти на тариф выше: https://profile.makscee.ru/vc/pay?tier=t3&mode=upgrade';
  for (const [type, sentence] of [['wallet_charge_required', unpaid], ['wallet_daily_charge_required', unpaid.replace('эту неделю', 'сегодня')], ['budget_exceeded', limit]]) {
    const failed402 = await answer(402, { error: { type, message: sentence } });
    assert.equal(failed402.stopReason, 'error');
    assert.equal(failed402.errorMessage, sentence);
    const reply = await messageEnd(failed402, 'tui');
    assert.ok(reply, type + ': the terminal UI must get the refusal as a reply, not an error Pi offers /bug for');
    assert.equal(reply.role, 'assistant');
    assert.notEqual(reply.stopReason, 'error');
    assert.equal(reply.errorMessage, undefined);
    assert.deepEqual(reply.content, [{ type: 'text', text: sentence }]);
    assert.equal(await messageEnd(failed402, 'rpc'), undefined, 'the desktop (RPC) keeps the error');
    // The next turn: the refusal is not something the model said.
    let sent;
    globalThis.fetch = async (_url, options) => { sent = options.body; return new Response('x', { status: 500 }); };
    const next = normalizeContext({ systemPrompt: 'probe', messages: [context.messages.at(-1), reply, { role: 'user', content: 'again', timestamp: 3 }] });
    const stream = provider.streamSimple(model, next);
    for await (const _ of stream) {}
    assert.ok(sent.includes('again'));
    assert.ok(!sent.includes(sentence.slice(0, 20)), type + ': the refusal reached the model as its own words');
  }
  for (const [status, body] of [[500, 'upstream exploded'], [402, 'not json'], [401, { error: { message: unpaid } }]]) {
    const failed = await answer(status, body);
    assert.equal(failed.stopReason, 'error');
    assert.equal(await messageEnd(failed, 'tui'), undefined, 'HTTP ' + status + ' is not a billing refusal and keeps Pi\'s error');
  }
  console.log(JSON.stringify({ scenario: 'billing-refusal-reply', pass: true }));
} catch (error) {
  failed++;
  console.error(JSON.stringify({ scenario: 'billing-refusal-reply', pass: false, error: error.message }));
}
process.exitCode = failed ? 1 : 0;

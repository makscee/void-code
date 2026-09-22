// The relay the bundled smoke talks to instead of the real one: one canned Responses stream, no
// network, no money, no account.
//
// It exists because registering a provider turned out to prove nothing about holding a conversation.
// The extension resolves `@earendil-works/pi-ai/compat` on disk and loads a file next to it, and it
// does that while BUILDING THE REQUEST -- so a bundle registers its provider, lists its models, and
// dies the moment somebody types. The smoke watched the door at the entrance while the one that
// mattered was further down the corridor.
//
// A separate process, and that is not tidiness: the smoke runs Pi through execFileSync, which holds
// the event loop, so a server inside the same process cannot answer while the child waits for it.
// Written the obvious way first, the child sat there until it timed out, and the request arrived in
// the log after the run had been killed.
import { createServer } from 'node:http';
import { URL } from 'node:url';

// What the model "says". The smoke greps for it, so it has to be something no other part of the
// output could produce by accident.
const reply = process.env.VC_SMOKE_RELAY_REPLY;
const authToken = process.env.VC_SMOKE_RELAY_AUTH_TOKEN;
const decisionJSON = process.env.VC_SMOKE_RELAY_DECISION_JSON;
if (!reply || !authToken || !decisionJSON) {
  process.stderr.write('pi-smoke-relay: reply, bearer, and V2 decision fixture are required\n');
  process.exit(1);
}
let decision;
try {
  decision = JSON.parse(decisionJSON);
} catch {
  process.stderr.write('pi-smoke-relay: V2 decision fixture is not JSON\n');
  process.exit(1);
}

// The shape of an OpenAI Responses stream, cut down to the smallest sequence that produces one text
// answer: created, an item to hold the text, the text, the item closed, the response completed. Read
// off pi's own processResponsesStream rather than guessed, and proved by running it against the
// unbundled Pi until the answer came back.
const message = { type: 'message', id: 'msg_smoke', role: 'assistant', status: 'completed', content: [{ type: 'output_text', text: reply, annotations: [] }] };
const events = [
  { type: 'response.created', response: { id: 'resp_smoke' } },
  { type: 'response.output_item.added', output_index: 0, item: { ...message, status: 'in_progress', content: [] } },
  { type: 'response.output_text.delta', output_index: 0, delta: reply },
  { type: 'response.output_item.done', output_index: 0, item: message },
  { type: 'response.completed', response: { id: 'resp_smoke', status: 'completed', output: [message], usage: { input_tokens: 1, output_tokens: 1, total_tokens: 2 } } },
];
const body = `${events.map((event) => `data: ${JSON.stringify(event)}\n\n`).join('')}data: [DONE]\n\n`;

// This is deliberately a tiny local authority/relay pair. The readback has the same opaque local
// origin as the turn, but its path and query remain the client contract under test. No production
// host, credential, or decision is implied by this fixture.
const server = createServer((request, response) => {
  const url = new URL(request.url ?? '/', 'http://127.0.0.1');
  const bearer = request.headers.authorization;
  if (url.pathname === '/v1/vc/me' && url.searchParams.get('fixture') === 'bundled-pi-smoke') {
    request.resume();
    if (bearer !== `Bearer ${authToken}`) {
      response.writeHead(401, { 'content-type': 'text/plain' });
      response.end('fixture bearer mismatch');
      return;
    }
    response.writeHead(200, { 'content-type': 'application/json', 'cache-control': 'no-store' });
    response.end(JSON.stringify(decision));
    return;
  }
  if (request.method === 'POST' && url.pathname === '/codex/responses') {
    request.resume();
    if (bearer !== `Bearer ${authToken}`) {
      response.writeHead(401, { 'content-type': 'text/plain' });
      response.end('fixture bearer mismatch');
      return;
    }
    response.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' });
    response.end(body);
    return;
  }
  request.resume();
  response.writeHead(404, { 'content-type': 'text/plain' });
  response.end('fixture route not found');
});

// Port 0: the runner picks. Printed on stdout because the parent has no other way to learn it, and a
// fixed port is how two runs of the same check on one machine collide.
server.listen(0, '127.0.0.1', () => process.stdout.write(`PORT ${server.address().port}\n`));

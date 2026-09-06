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

// What the model "says". The smoke greps for it, so it has to be something no other part of the
// output could produce by accident.
const reply = process.env.VC_SMOKE_RELAY_REPLY;
if (!reply) {
  process.stderr.write('pi-smoke-relay: VC_SMOKE_RELAY_REPLY is empty, so there is nothing to answer with\n');
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

// Every path answers the same way. The smoke is not checking the relay's routing -- it is checking
// that Pi can carry a turn end to end -- and a 404 from here would read as a broken bundle.
const server = createServer((request, response) => {
  request.resume();
  response.writeHead(200, { 'content-type': 'text/event-stream', 'cache-control': 'no-cache' });
  response.end(body);
});

// Port 0: the runner picks. Printed on stdout because the parent has no other way to learn it, and a
// fixed port is how two runs of the same check on one machine collide.
server.listen(0, '127.0.0.1', () => process.stdout.write(`PORT ${server.address().port}\n`));

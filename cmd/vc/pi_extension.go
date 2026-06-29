package main

const piVoidCodexExtensionSource = `import type {
	AssistantMessage,
	AssistantMessageEventStream,
	Context,
	Model,
	SimpleStreamOptions,
} from "@earendil-works/pi-ai";
import { createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const PROVIDER_ID = "void-codex";
const MODEL_ID = "gpt-5.4";

export default function (pi: ExtensionAPI) {
	pi.registerProvider(PROVIDER_ID, {
		name: "Void Codex relay",
		baseUrl: "$VC_RELAY_URL",
		apiKey: "$VC_AUTH_TOKEN",
		api: "void-codex-sse",
		models: [
			{
				id: MODEL_ID,
				name: "GPT-5.4 via Void relay",
				reasoning: true,
				thinkingLevelMap: { xhigh: "xhigh", minimal: "low" },
				input: ["text", "image"],
				cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
				contextWindow: 272000,
				maxTokens: 128000,
			},
		],
		streamSimple: streamVoidCodex,
	});
}

function streamVoidCodex(
	model: Model<any>,
	context: Context,
	options?: SimpleStreamOptions,
): AssistantMessageEventStream {
	const stream = createAssistantMessageEventStream();

	(async () => {
		const output: AssistantMessage = {
			role: "assistant",
			content: [],
			api: model.api,
			provider: model.provider,
			model: model.id,
			usage: {
				input: 0,
				output: 0,
				cacheRead: 0,
				cacheWrite: 0,
				totalTokens: 0,
				cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, total: 0 },
			},
			stopReason: "stop",
			timestamp: Date.now(),
		};

		let textIndex = -1;
		try {
			stream.push({ type: "start", partial: output });
			const relayURL = requiredEnv("VC_RELAY_URL").replace(/\/+$/, "") + "/codex/responses";
			const token = requiredEnv("VC_AUTH_TOKEN");
			const providerID = requiredEnv("VC_RELAY_PROVIDER_ID");
			const body = JSON.stringify(buildCodexBody(model, context, options));

			const response = await fetch(relayURL, {
				method: "POST",
				headers: {
					"accept": "text/event-stream",
					"content-type": "application/json",
					"authorization": "Bearer " + token,
					"x-void-provider": providerID,
				},
				body,
				signal: options?.signal,
			});
			await options?.onResponse?.({ status: response.status, headers: headersToRecord(response.headers) }, model);
			if (!response.ok) {
				throw new Error("Void relay Codex request failed: HTTP " + response.status + ": " + (await response.text()));
			}
			if (!response.body) throw new Error("Void relay Codex response had no body");

			for await (const event of parseSSE(response, options?.signal)) {
				if (event.type === "error") {
					throw new Error(event.message || event.error?.message || JSON.stringify(event));
				}
				if (event.type === "response.failed") {
					throw new Error(event.response?.error?.message || JSON.stringify(event));
				}
				if (event.type === "response.output_text.delta" && typeof event.delta === "string") {
					if (textIndex < 0) textIndex = startText(output, stream);
					const block = output.content[textIndex];
					if (block.type === "text") block.text += event.delta;
					stream.push({ type: "text_delta", contentIndex: textIndex, delta: event.delta, partial: output });
				}
				if (event.type === "response.output_item.done" && textIndex < 0) {
					const text = extractDoneText(event.item);
					if (text) {
						textIndex = startText(output, stream);
						const block = output.content[textIndex];
						if (block.type === "text") block.text = text;
						stream.push({ type: "text_delta", contentIndex: textIndex, delta: text, partial: output });
					}
				}
				if (event.type === "response.completed" || event.type === "response.done" || event.type === "response.incomplete") {
					applyUsage(output, event.response?.usage);
				}
			}

			if (textIndex >= 0) {
				const block = output.content[textIndex];
				stream.push({
					type: "text_end",
					contentIndex: textIndex,
					content: block.type === "text" ? block.text : "",
					partial: output,
				});
			}
			stream.push({ type: "done", reason: output.stopReason as "stop", message: output });
			stream.end();
		} catch (error) {
			output.stopReason = options?.signal?.aborted ? "aborted" : "error";
			output.errorMessage = error instanceof Error ? error.message : String(error);
			stream.push({ type: "error", reason: output.stopReason, error: output });
			stream.end();
		}
	})();

	return stream;
}

function requiredEnv(name: string): string {
	const value = process.env[name];
	if (!value) throw new Error(name + " is required for void-codex");
	return value;
}

function buildCodexBody(model: Model<any>, context: Context, options?: SimpleStreamOptions): Record<string, unknown> {
	const body: Record<string, unknown> = {
		model: model.id,
		store: false,
		stream: true,
		instructions: context.systemPrompt || "You are a helpful coding assistant.",
		input: context.messages.map(toCodexInput),
		text: { verbosity: "low" },
		include: ["reasoning.encrypted_content"],
		tool_choice: "auto",
		parallel_tool_calls: true,
	};
	if (context.tools?.length) {
		body.tools = context.tools.map((tool) => ({
			type: "function",
			name: tool.name,
			description: tool.description,
			parameters: tool.parameters,
			strict: null,
		}));
	}
	if (options?.maxTokens) body.max_output_tokens = options.maxTokens;
	return body;
}

function toCodexInput(message: any): Record<string, unknown> {
	if (message.role === "assistant") {
		return {
			type: "message",
			role: "assistant",
			content: (message.content || []).filter((c: any) => c.type === "text").map((c: any) => ({ type: "output_text", text: c.text || "" })),
		};
	}
	if (message.role === "toolResult") {
		return {
			type: "function_call_output",
			call_id: message.toolCallId,
			output: stringifyContent(message.content),
		};
	}
	return {
		type: "message",
		role: "user",
		content: normalizeUserContent(message.content),
	};
}

function normalizeUserContent(content: any): Array<Record<string, string>> {
	if (typeof content === "string") return [{ type: "input_text", text: content }];
	if (!Array.isArray(content)) return [{ type: "input_text", text: String(content ?? "") }];
	return content.map((part) => {
		if (part.type === "image") return { type: "input_image", image_url: "data:" + part.mimeType + ";base64," + part.data };
		return { type: "input_text", text: part.text || "" };
	});
}

function stringifyContent(content: any): string {
	if (typeof content === "string") return content;
	if (!Array.isArray(content)) return JSON.stringify(content ?? "");
	return content.map((part) => part.text || JSON.stringify(part)).join("\n");
}

function startText(output: AssistantMessage, stream: AssistantMessageEventStream): number {
	output.content.push({ type: "text", text: "" });
	const contentIndex = output.content.length - 1;
	stream.push({ type: "text_start", contentIndex, partial: output });
	return contentIndex;
}

function extractDoneText(item: any): string {
	if (!item || !Array.isArray(item.content)) return "";
	return item.content
		.filter((part: any) => part.type === "output_text" && typeof part.text === "string")
		.map((part: any) => part.text)
		.join("");
}

function applyUsage(output: AssistantMessage, usage: any) {
	if (!usage) return;
	output.usage.input = usage.input_tokens || usage.input || 0;
	output.usage.output = usage.output_tokens || usage.output || 0;
	output.usage.cacheRead = usage.input_tokens_details?.cached_tokens || usage.cache_read_tokens || 0;
	output.usage.cacheWrite = usage.cache_write_tokens || 0;
	output.usage.totalTokens = output.usage.input + output.usage.output + output.usage.cacheRead + output.usage.cacheWrite;
}

async function* parseSSE(response: Response, signal?: AbortSignal): AsyncGenerator<any> {
	const reader = response.body!.getReader();
	const decoder = new TextDecoder();
	let buffer = "";
	try {
		while (true) {
			if (signal?.aborted) throw new Error("Request was aborted");
			const { done, value } = await reader.read();
			if (done) break;
			buffer += decoder.decode(value, { stream: true });
			let idx = buffer.indexOf("\n\n");
			while (idx >= 0) {
				const chunk = buffer.slice(0, idx);
				buffer = buffer.slice(idx + 2);
				const data = chunk.split("\n").filter((line) => line.startsWith("data:")).map((line) => line.slice(5).trim()).join("\n").trim();
				if (data && data !== "[DONE]") yield JSON.parse(data);
				idx = buffer.indexOf("\n\n");
			}
		}
	} finally {
		try { reader.releaseLock(); } catch {}
	}
}

function headersToRecord(headers: Headers): Record<string, string> {
	const out: Record<string, string> = {};
	headers.forEach((value, key) => { out[key] = value; });
	return out;
}
`

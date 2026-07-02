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

const CODEX_PROVIDER_ID = "void-codex";
const CODEX_MODEL_ID = "gpt-5.5";
const DEEPSEEK_PROVIDER_ID = "void-deepseek";
const DEEPSEEK_MODEL_ID = "claude-sonnet-4-6";

export default function (pi: ExtensionAPI) {
	pi.registerProvider(CODEX_PROVIDER_ID, {
		name: "Void ChatGPT relay",
		baseUrl: "$VC_RELAY_URL",
		apiKey: "$VC_AUTH_TOKEN",
		api: "void-codex-sse",
		models: [
			{
				id: CODEX_MODEL_ID,
				name: "GPT-5.5 via Void relay",
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

	pi.registerProvider(DEEPSEEK_PROVIDER_ID, {
		name: "Void DeepSeek relay",
		baseUrl: requiredEnv("VC_RELAY_URL"),
		apiKey: "$VC_AUTH_TOKEN",
		authHeader: true,
		api: "anthropic-messages",
		headers: { "x-void-provider": "$VC_RELAY_PROVIDER_ID" },
		models: [
			{
				id: DEEPSEEK_MODEL_ID,
				name: "Claude Sonnet 4.6 via Void DeepSeek relay",
				reasoning: true,
				input: ["text", "image"],
				cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
				contextWindow: 200000,
				maxTokens: 64000,
			},
		],
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
		const toolSlots = new Map<string, number>();
		const toolArgumentBuffers = new Map<string, string>();
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
				if (event.type === "response.output_item.added" && event.item?.type === "function_call") {
					const key = outputItemKey(event);
					if (!toolSlots.has(key)) startToolCall(output, stream, event.item, key, toolSlots, toolArgumentBuffers);
				}
				if (event.type === "response.function_call_arguments.delta" && typeof event.delta === "string") {
					const key = outputItemKey(event);
					const contentIndex = toolSlots.get(key);
					if (contentIndex !== undefined) {
						const nextArguments = (toolArgumentBuffers.get(key) || "") + event.delta;
						toolArgumentBuffers.set(key, nextArguments);
						const block = output.content[contentIndex];
						if (block.type === "toolCall") block.arguments = parseJsonObject(nextArguments);
						stream.push({ type: "toolcall_delta", contentIndex, delta: event.delta, partial: output });
					}
				}
				if (event.type === "response.function_call_arguments.done" && typeof event.arguments === "string") {
					const key = outputItemKey(event);
					const contentIndex = toolSlots.get(key);
					if (contentIndex !== undefined) {
						const previousArguments = toolArgumentBuffers.get(key) || "";
						toolArgumentBuffers.set(key, event.arguments);
						const block = output.content[contentIndex];
						if (block.type === "toolCall") block.arguments = parseJsonObject(event.arguments);
						if (event.arguments.startsWith(previousArguments)) {
							const delta = event.arguments.slice(previousArguments.length);
							if (delta) stream.push({ type: "toolcall_delta", contentIndex, delta, partial: output });
						}
					}
				}
				if (event.type === "response.output_item.done" && event.item?.type === "function_call") {
					const key = outputItemKey(event);
					if (!toolSlots.has(key)) startToolCall(output, stream, event.item, key, toolSlots, toolArgumentBuffers);
					const contentIndex = toolSlots.get(key);
					if (contentIndex !== undefined) {
						const previousArguments = toolArgumentBuffers.get(key) || "";
						const finalArguments = typeof event.item.arguments === "string" ? event.item.arguments : (previousArguments || "{}");
						toolArgumentBuffers.set(key, finalArguments);
						const block = output.content[contentIndex];
						if (block.type === "toolCall") {
							if (finalArguments.startsWith(previousArguments)) {
								const delta = finalArguments.slice(previousArguments.length);
								if (delta) stream.push({ type: "toolcall_delta", contentIndex, delta, partial: output });
							}
							block.id = buildToolCallID(event.item);
							block.name = event.item.name || block.name;
							block.arguments = parseJsonObject(finalArguments);
							output.stopReason = "toolUse";
							stream.push({ type: "toolcall_end", contentIndex, toolCall: block, partial: output });
						}
						toolSlots.delete(key);
						toolArgumentBuffers.delete(key);
					}
				}
				if (event.type === "response.output_item.done" && event.item?.type !== "function_call" && textIndex < 0) {
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
			stream.push({ type: "done", reason: output.stopReason as "stop" | "length" | "toolUse", message: output });
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
	if (!value) throw new Error(name + " is required for Pi void relay");
	return value;
}

function buildCodexBody(model: Model<any>, context: Context, options?: SimpleStreamOptions): Record<string, unknown> {
	const body: Record<string, unknown> = {
		model: model.id,
		store: false,
		stream: true,
		instructions: context.systemPrompt || "You are a helpful coding assistant.",
		input: context.messages.flatMap(toCodexInput),
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

function toCodexInput(message: any): Array<Record<string, unknown>> {
	if (message.role === "assistant") {
		const items: Array<Record<string, unknown>> = [];
		let textContent: Array<Record<string, string>> = [];
		const flushText = () => {
			if (!textContent.length) return;
			items.push({ type: "message", role: "assistant", content: textContent });
			textContent = [];
		};
		for (const block of message.content || []) {
			if (block.type === "text") {
				textContent.push({ type: "output_text", text: block.text || "" });
			} else if (block.type === "toolCall") {
				flushText();
				const [callID, itemID] = splitToolCallID(block.id);
				const item: Record<string, unknown> = {
					type: "function_call",
					call_id: callID,
					name: block.name,
					arguments: JSON.stringify(block.arguments || {}),
				};
				if (itemID) item.id = itemID;
				items.push(item);
			}
		}
		flushText();
		return items;
	}
	if (message.role === "toolResult") {
		const [callID] = splitToolCallID(message.toolCallId);
		return [{
			type: "function_call_output",
			call_id: callID,
			output: stringifyContent(message.content),
		}];
	}
	return [{
		type: "message",
		role: "user",
		content: normalizeUserContent(message.content),
	}];
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

function startToolCall(
	output: AssistantMessage,
	stream: AssistantMessageEventStream,
	item: any,
	key: string,
	toolSlots: Map<string, number>,
	toolArgumentBuffers: Map<string, string>,
): number {
	const initialArguments = typeof item?.arguments === "string" ? item.arguments : "";
	output.content.push({
		type: "toolCall",
		id: buildToolCallID(item),
		name: item?.name || "",
		arguments: parseJsonObject(initialArguments),
	});
	const contentIndex = output.content.length - 1;
	toolSlots.set(key, contentIndex);
	toolArgumentBuffers.set(key, initialArguments);
	stream.push({ type: "toolcall_start", contentIndex, partial: output });
	if (initialArguments) stream.push({ type: "toolcall_delta", contentIndex, delta: initialArguments, partial: output });
	return contentIndex;
}

function outputItemKey(event: any): string {
	if (event.output_index !== undefined && event.output_index !== null) return "index:" + event.output_index;
	if (event.item_id) return "item:" + event.item_id;
	if (event.item?.id) return "item:" + event.item.id;
	if (event.item?.call_id) return "call:" + event.item.call_id;
	return "unknown";
}

function buildToolCallID(item: any): string {
	const callID = String(item?.call_id || item?.id || "call");
	const itemID = item?.id ? String(item.id) : "";
	return itemID && itemID !== callID ? callID + "|" + itemID : callID;
}

function splitToolCallID(id: any): [string, string | undefined] {
	const raw = String(id || "");
	const separator = raw.indexOf("|");
	if (separator < 0) return [raw, undefined];
	return [raw.slice(0, separator), raw.slice(separator + 1) || undefined];
}

function parseJsonObject(text: string): Record<string, any> {
	if (!text) return {};
	try {
		const parsed = JSON.parse(text);
		if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) return parsed;
	} catch {}
	return {};
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

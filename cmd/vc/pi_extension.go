package main

const piVoidCodexExtensionSource = `// void-code-managed-pi-extension:v1
import { execFileSync } from "node:child_process";
import type {
	AssistantMessage,
	AssistantMessageEventStream,
	Context,
	Model,
	SimpleStreamOptions,
} from "@earendil-works/pi-ai";
import { clampThinkingLevel, createAssistantMessageEventStream } from "@earendil-works/pi-ai";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

const CODEX_PROVIDER_ID = "void-codex";
const CODEX_MODEL_ID = "gpt-5.6-terra";
const DEEPSEEK_PROVIDER_ID = "void-deepseek";
const DEEPSEEK_MODEL_ID = "deepseek/deepseek-v4-pro";

interface BootstrapProvider {
	kind: "codex" | "deepseek";
	relayProviderId: string;
	models: string[];
}
interface Bootstrap {
	version: number;
	relayUrl: string;
	authToken: string;
	providers: BootstrapProvider[];
}
let activeBootstrap: Bootstrap | undefined;

export default function (pi: ExtensionAPI) {
	const bootstrap = loadBootstrap();
	if (!bootstrap) return;
	activeBootstrap = bootstrap;
	for (const provider of bootstrap.providers) {
		if (provider.kind === "codex") {
			const allowed = new Set([CODEX_MODEL_ID, "gpt-5.6-sol", "gpt-5.6-luna"]);
			const models = provider.models.filter((id) => allowed.has(id)).map((id) => codexModel(id, codexName(id)));
			if (models.length === 0) continue;
			pi.registerProvider(CODEX_PROVIDER_ID, {
				name: "Void ChatGPT relay",
				baseUrl: bootstrap.relayUrl,
				apiKey: bootstrap.authToken,
				api: "void-codex-sse",
				models,
				streamSimple: streamVoidCodex,
			});
		}
		if (provider.kind === "deepseek") {
			const allowed = new Set([DEEPSEEK_MODEL_ID, "deepseek/deepseek-v4-flash"]);
			const models = provider.models.filter((id) => allowed.has(id)).map((id) => deepseekModel(id, deepseekName(id), 200000, 64000));
			if (models.length === 0) continue;
			pi.registerProvider(DEEPSEEK_PROVIDER_ID, {
				name: "Void DeepSeek relay",
				baseUrl: bootstrap.relayUrl,
				apiKey: bootstrap.authToken,
				authHeader: true,
				api: "anthropic-messages",
				headers: { "x-void-provider": provider.relayProviderId },
				models,
			});
		}
	}
}

function loadBootstrap(): Bootstrap | undefined {
	try {
		const kind = process.env.VC_PI_PROVIDER_KIND || (process.env.VC_RELAY_PROVIDER_ID === "deepseek" ? "deepseek" : "codex");
		if (process.env.VC_RELAY_URL && process.env.VC_AUTH_TOKEN && process.env.VC_RELAY_PROVIDER_ID && (kind === "codex" || kind === "deepseek")) {
			return {
				version: 1,
				relayUrl: process.env.VC_RELAY_URL,
				authToken: process.env.VC_AUTH_TOKEN,
				providers: [{
					kind,
					relayProviderId: process.env.VC_RELAY_PROVIDER_ID,
					models: kind === "codex" ? [CODEX_MODEL_ID, "gpt-5.6-sol", "gpt-5.6-luna"] : [DEEPSEEK_MODEL_ID, "deepseek/deepseek-v4-flash"],
				}],
			};
		}
		const raw = execFileSync("vc", ["pi-bootstrap"], { encoding: "utf8", stdio: ["ignore", "pipe", "pipe"], timeout: 15000 });
		const value = JSON.parse(raw) as Bootstrap;
		if (value.version !== 1 || !value.relayUrl || !value.authToken || !Array.isArray(value.providers)) throw new Error("invalid bootstrap response");
		return value;
	} catch (error) {
		console.error("void-code: managed Pi provider unavailable; run vc login, then vc (" + (error instanceof Error ? error.message : String(error)) + ")");
		return undefined;
	}
}

function codexName(id: string): string {
	if (id === "gpt-5.6-sol") return "GPT-5.6 Sol via Void relay";
	if (id === "gpt-5.6-luna") return "GPT-5.6 Luna via Void relay";
	return "GPT-5.6 Terra via Void relay";
}

function deepseekName(id: string): string {
	return id.endsWith("flash") ? "DeepSeek V4 Flash via Void relay" : "DeepSeek V4 Pro via Void relay";
}

function codexModel(id: string, name: string): Model<any> {
	return {
		id,
		name,
		reasoning: true,
		thinkingLevelMap: { xhigh: "xhigh", minimal: "low" },
		input: ["text", "image"],
		cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
		// This private ChatGPT backend rejects around 385k tokens despite the
		// public API's 1.05M claim. Keep Pi's effective window conservative so
		// its default 16,384-token reserve compacts at 255,616 tokens.
		contextWindow: 272000,
		maxTokens: 128000,
	};
}

function deepseekModel(id: string, name: string, contextWindow: number, maxTokens: number): Model<any> {
	return {
		id,
		name,
		reasoning: true,
		input: ["text"],
		cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
		contextWindow,
		maxTokens,
	};
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

		try {
			if (!activeBootstrap) throw new Error("Void provider bootstrap is unavailable");
			const relayURL = activeBootstrap.relayUrl.replace(/\/+$/, "") + "/codex/responses";
			const token = activeBootstrap.authToken;
			const providerID = activeBootstrap.providers.find((provider) => provider.kind === "codex")?.relayProviderId;
			if (!providerID) throw new Error("Void Codex provider grant is unavailable");
			let requestBody = await buildCodexBody(model, context, options);
			const nextBody = await options?.onPayload?.(requestBody, model);
			if (nextBody !== undefined) requestBody = nextBody as Record<string, unknown>;
			requestBody = await sanitizeCodexBody(requestBody);
			const body = JSON.stringify(requestBody);
			const headers: Record<string, string> = {
				"accept": "text/event-stream",
				"content-type": "application/json",
				"authorization": "Bearer " + token,
				"x-void-provider": providerID,
			};
			if (options?.sessionId) headers["x-pi-session-id"] = options.sessionId;

			const response = await fetch(relayURL, {
				method: "POST",
				headers,
				body,
				signal: options?.signal,
			});
			await options?.onResponse?.({ status: response.status, headers: headersToRecord(response.headers) }, model);
			if (!response.ok) {
				throw new Error("Void relay Codex request failed: HTTP " + response.status + ": " + (await response.text()));
			}
			if (!response.body) throw new Error("Void relay Codex response had no body");

			stream.push({ type: "start", partial: output });
			const { processResponsesStream } = await openAIResponsesShared();
			await processResponsesStream(parseSSE(response, options?.signal), output, stream, model);
			normalizeUsage(output);
			if (options?.signal?.aborted) throw new Error("Request was aborted");
			stream.push({ type: "done", reason: output.stopReason as "stop" | "length" | "toolUse", message: output });
			stream.end();
		} catch (error) {
			for (const block of output.content) delete (block as any).partialJson;
			output.stopReason = options?.signal?.aborted ? "aborted" : "error";
			output.errorMessage = error instanceof Error ? error.message : String(error);
			stream.push({ type: "error", reason: output.stopReason, error: output });
			stream.end();
		}
	})();

	return stream;
}

function promptCacheKey(sessionId?: string): string | undefined {
	if (!sessionId) return undefined;
	return sessionId.slice(0, 64);
}

const CODEX_IMAGE_MAX_BASE64_BYTES = 1.5 * 1024 * 1024;

async function sanitizeCodexBody(body: Record<string, unknown>): Promise<Record<string, unknown>> {
	const out = { ...body };
	// Pi compaction/summarization may add the standard OpenAI Responses cap, but
	// ChatGPT Codex backend rejects it with "Unsupported parameter: max_output_tokens".
	delete out.max_output_tokens;
	await shrinkCodexImages(out);
	return out;
}

async function shrinkCodexImages(node: unknown): Promise<void> {
	if (Array.isArray(node)) {
		let previousText: Record<string, unknown> | undefined;
		for (const item of node) {
			if (item && typeof item === "object") {
				const record = item as Record<string, unknown>;
				if (record.type === "input_text") previousText = record;
				if (record.type === "input_image" && typeof record.image_url === "string") {
					const resized = await shrinkDataImageUrl(record.image_url);
					record.image_url = resized.url;
					if (resized.wasShrunk && previousText && typeof previousText.text === "string") {
						previousText.text = updateImageDeliveryNote(previousText.text, resized);
					}
				}
			}
			await shrinkCodexImages(item);
		}
		return;
	}
	if (!node || typeof node !== "object") return;
	for (const value of Object.values(node as Record<string, unknown>)) await shrinkCodexImages(value);
}

interface ShrinkResult {
	url: string;
	mimeType: string;
	width: number;
	height: number;
	wasShrunk: boolean;
}

async function shrinkDataImageUrl(url: string): Promise<ShrinkResult> {
	const match = /^data:([^;,]+);base64,([\s\S]*)$/.exec(url);
	if (!match) return { url, mimeType: "image/unknown", width: 0, height: 0, wasShrunk: false };
	const mimeType = match[1];
	const data = match[2];
	if (Buffer.byteLength(data, "utf8") <= CODEX_IMAGE_MAX_BASE64_BYTES) {
		return { url, mimeType, width: 0, height: 0, wasShrunk: false };
	}
	const { resizeImage } = await import("@earendil-works/pi-coding-agent");
	const bytes = Uint8Array.from(Buffer.from(data, "base64"));
	const resized = await resizeImage(bytes, mimeType, { maxBytes: CODEX_IMAGE_MAX_BASE64_BYTES });
	if (!resized) {
		throw new Error("Image could not be resized below the Void Codex image guard; run sips -Z 1600 --setProperty format jpeg --setProperty formatOptions 70 INPUT --out OUTPUT.jpg and read OUTPUT.jpg.");
	}
	return {
		url: "data:" + resized.mimeType + ";base64," + resized.data,
		mimeType: resized.mimeType,
		width: resized.width,
		height: resized.height,
		wasShrunk: true,
	};
}

function updateImageDeliveryNote(text: string, resized: ShrinkResult): string {
	const note = /\[Image: original (\d+)x(\d+), displayed at \d+x\d+\. Multiply coordinates by [0-9.]+ to map to original image\.\]/;
	return text.replace(note, (_match, originalWidth: string, originalHeight: string) => {
		const scale = Number(originalWidth) / resized.width;
		return "[Image: original " + originalWidth + "x" + originalHeight + ", delivered " + resized.width + "x" + resized.height + " " + resized.mimeType + ". Multiply coordinates by " + scale.toFixed(2) + " to map to original image.]";
	});
}

function normalizeUsage(output: AssistantMessage) {
	const usage = output.usage;
	usage.totalTokens = usage.input + usage.output + usage.cacheRead + usage.cacheWrite;
}

async function buildCodexBody(model: Model<any>, context: Context, options?: SimpleStreamOptions): Promise<Record<string, unknown>> {
	const { convertResponsesMessages, convertResponsesTools } = await openAIResponsesShared();
	const body: Record<string, unknown> = {
		model: model.id,
		store: false,
		stream: true,
		instructions: context.systemPrompt || "You are a helpful assistant.",
		input: convertResponsesMessages(model, context, new Set(["openai", "openai-codex", "opencode"]), { includeSystemPrompt: false }),
		text: { verbosity: (options as any)?.textVerbosity || "low" },
		include: ["reasoning.encrypted_content"],
		prompt_cache_key: promptCacheKey(options?.sessionId),
		tool_choice: "auto",
		parallel_tool_calls: true,
	};
	if ((options as any)?.temperature !== undefined) body.temperature = (options as any).temperature;
	if ((options as any)?.serviceTier !== undefined) body.service_tier = (options as any).serviceTier;
	if (context.tools && context.tools.length > 0) body.tools = convertResponsesTools(context.tools, { strict: null });
	const clampedReasoning = options?.reasoning ? clampThinkingLevel(model, options.reasoning) : undefined;
	const reasoningEffort = clampedReasoning === "off" ? undefined : clampedReasoning;
	if (reasoningEffort !== undefined) {
		const effort = reasoningEffort === "none"
			? (model.thinkingLevelMap?.off ?? "none")
			: (model.thinkingLevelMap?.[reasoningEffort] ?? reasoningEffort);
		if (effort !== null) body.reasoning = { effort, summary: (options as any)?.reasoningSummary ?? "auto" };
	}
	return body;
}

async function openAIResponsesShared(): Promise<any> {
	const compatUrl = await import.meta.resolve("@earendil-works/pi-ai/compat");
	return import(new URL("./api/openai-responses-shared.js", compatUrl).href);
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
				if (data && data !== "[DONE]") yield normalizeSSEError(JSON.parse(data));
				idx = buffer.indexOf("\n\n");
			}
		}
	} finally {
		try { reader.releaseLock(); } catch {}
	}
}

function normalizeSSEError(event: any): any {
	if (event?.type !== "error" || !event.error || typeof event.error !== "object") return event;
	return {
		...event,
		code: event.code ?? event.error.code,
		message: event.message ?? event.error.message,
	};
}

function headersToRecord(headers: Headers): Record<string, string> {
	const out: Record<string, string> = {};
	headers.forEach((value, key) => { out[key] = value; });
	return out;
}
`

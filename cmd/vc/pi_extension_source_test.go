package main

import (
	"strings"
	"testing"
)

func TestPiVoidCodexExtensionSourceDelegatesResponsesParsingToPiNativeHelper(t *testing.T) {
	required := []string{
		`convertResponsesMessages`,
		`convertResponsesTools`,
		`processResponsesStream`,
		`const { processResponsesStream } = await openAIResponsesShared()`,
		`await processResponsesStream(parseSSE(response, options?.signal), output, stream, model)`,
		`reason: output.stopReason as "stop" | "length" | "toolUse"`,
	}
	for _, want := range required {
		if !strings.Contains(piVoidCodexExtensionSource, want) {
			t.Fatalf("Pi Codex extension source missing %q", want)
		}
	}
}

func TestPiVoidCodexExtensionSourceSendsSessionCacheKeyAndAttributionHeader(t *testing.T) {
	required := []string{
		`function promptCacheKey(sessionId?: string): string | undefined`,
		`return sessionId.slice(0, 64);`,
		`prompt_cache_key: promptCacheKey(options?.sessionId)`,
		`headers["x-pi-session-id"] = options.sessionId`,
	}
	for _, want := range required {
		if !strings.Contains(piVoidCodexExtensionSource, want) {
			t.Fatalf("Pi Codex extension source missing %q", want)
		}
	}
}

func TestPiVoidCodexExtensionSourceMatchesNativeCodexRequestShape(t *testing.T) {
	required := []string{
		`instructions: context.systemPrompt || "You are a helpful assistant."`,
		`input: convertResponsesMessages(model, context, new Set(["openai", "openai-codex", "opencode"]), { includeSystemPrompt: false })`,
		`text: { verbosity: (options as any)?.textVerbosity || "low" }`,
		`prompt_cache_key: promptCacheKey(options?.sessionId)`,
		`body.tools = convertResponsesTools(context.tools, { strict: null })`,
		`body.reasoning = { effort, summary: (options as any)?.reasoningSummary ?? "auto" }`,
	}
	for _, want := range required {
		if !strings.Contains(piVoidCodexExtensionSource, want) {
			t.Fatalf("Pi Codex extension source missing %q", want)
		}
	}
}

func TestPiVoidCodexExtensionSourceSanitizesPiCompactionPayload(t *testing.T) {
	required := []string{
		`requestBody = await sanitizeCodexBody(requestBody)`,
		`delete out.max_output_tokens`,
		`CODEX_IMAGE_MAX_BASE64_BYTES`,
		`await shrinkCodexImages(out)`,
		`resizeImage(bytes, mimeType, { maxBytes: CODEX_IMAGE_MAX_BASE64_BYTES })`,
		`updateImageDeliveryNote`,
		`delivered " + resized.width + "x" + resized.height + " " + resized.mimeType`,
		`normalizeUsage(output)`,
		`usage.totalTokens = usage.input + usage.output + usage.cacheRead + usage.cacheWrite`,
	}
	for _, want := range required {
		if !strings.Contains(piVoidCodexExtensionSource, want) {
			t.Fatalf("Pi Codex extension source missing %q", want)
		}
	}
}

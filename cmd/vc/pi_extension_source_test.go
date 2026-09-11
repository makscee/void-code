package main

import (
	"strings"
	"testing"
)

// The embedded transport keeps every OpenAI model while registering no retired provider.
func TestPiManagedOpenAIOnlyExtensionContract(t *testing.T) {
	if piDefaultProvider != "void-codex" || piDefaultModel != "gpt-5.6-terra" {
		t.Fatalf("fresh-user default = %s/%s, want void-codex/gpt-5.6-terra", piDefaultProvider, piDefaultModel)
	}

	for _, want := range []string{
		`if (provider.kind === "codex")`,
		`"gpt-6-astra"`,
		`if (id === "gpt-6-astra") return "GPT-6 Astra via Void relay";`,
	} {
		if !strings.Contains(piVoidCodexExtensionSource, want) {
			t.Errorf("the managed Pi extension is missing Astra contract %q", want)
		}
	}
	for _, forbidden := range []string{
		`pi.registerProvider(DEEPSEEK_PROVIDER_ID`,
		`pi.registerProvider("void-deepseek"`,
	} {
		if strings.Contains(piVoidCodexExtensionSource, forbidden) {
			t.Errorf("managed Pi extension still registers the retired provider through %q", forbidden)
		}
	}
}

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

func TestPiVoidCodexExtensionSourceUsesPrivateBackendWindowAndNormalizesNestedErrors(t *testing.T) {
	required := []string{
		`public API's 1.05M claim`,
		`default 16,384-token reserve compacts at 255,616 tokens`,
		`contextWindow: 272000`,
		`yield normalizeSSEError(JSON.parse(data))`,
		`code: event.code ?? event.error.code`,
		`message: event.message ?? event.error.message`,
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

package main

import (
	"strings"
	"testing"
)

func TestPiManagedAstraExtensionContract(t *testing.T) {
	if piDefaultProvider != "void-codex" || piDefaultModel != "gpt-5.6-terra" {
		t.Fatalf("fresh-user default = %s/%s, want void-codex/gpt-5.6-terra", piDefaultProvider, piDefaultModel)
	}

	codexStart := strings.Index(piVoidCodexExtensionSource, `if (provider.kind === "codex")`)
	deepseekStart := strings.Index(piVoidCodexExtensionSource, `if (provider.kind === "deepseek")`)
	if codexStart < 0 || deepseekStart <= codexStart {
		t.Fatal("cannot locate the Codex registration branch in the managed Pi extension")
	}
	codexRegistration := piVoidCodexExtensionSource[codexStart:deepseekStart]
	if !strings.Contains(codexRegistration, `"gpt-6-astra"`) {
		t.Error("the managed Pi extension filters gpt-6-astra out before pi.registerProvider, so it cannot appear in --list-models or /model")
	}

	nameStart := strings.Index(piVoidCodexExtensionSource, "function codexName(")
	nameEnd := strings.Index(piVoidCodexExtensionSource, "function deepseekName(")
	if nameStart < 0 || nameEnd <= nameStart {
		t.Fatal("cannot locate codexName in the managed Pi extension")
	}
	if want := `if (id === "gpt-6-astra") return "GPT-6 Astra via Void relay";`; !strings.Contains(piVoidCodexExtensionSource[nameStart:nameEnd], want) {
		t.Errorf("the managed Pi extension is missing Astra's display-name mapping %q", want)
	}

	for _, unchanged := range []string{
		`const DEEPSEEK_MODEL_ID = "deepseek/deepseek-v4-pro";`,
		`const allowed = new Set([DEEPSEEK_MODEL_ID, "deepseek/deepseek-v4-flash"]);`,
		`return id.endsWith("flash") ? "DeepSeek V4 Flash via Void relay" : "DeepSeek V4 Pro via Void relay";`,
	} {
		if !strings.Contains(piVoidCodexExtensionSource, unchanged) {
			t.Errorf("DeepSeek extension contract changed: missing %q", unchanged)
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

package main

import (
	"strings"
	"testing"
)

func TestPiVoidCodexExtensionSourceHandlesResponsesFunctionCalls(t *testing.T) {
	required := []string{
		`response.output_item.added`,
		`event.item?.type === "function_call"`,
		`response.function_call_arguments.delta`,
		`response.function_call_arguments.done`,
		`type: "toolcall_start"`,
		`type: "toolcall_delta"`,
		`type: "toolcall_end"`,
		`output.stopReason = "toolUse"`,
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
		`body.prompt_cache_key = cacheKey`,
		`headers["x-pi-session-id"] = options.sessionId`,
	}
	for _, want := range required {
		if !strings.Contains(piVoidCodexExtensionSource, want) {
			t.Fatalf("Pi Codex extension source missing %q", want)
		}
	}
}

func TestPiVoidCodexExtensionSourceMapsAssistantToolCallsWithFlatMap(t *testing.T) {
	required := []string{
		`input: context.messages.flatMap(toCodexInput)`,
		`function toCodexInput(message: any): Array<Record<string, unknown>>`,
		`block.type === "toolCall"`,
		`type: "function_call"`,
		`call_id: callID`,
		`arguments: JSON.stringify(block.arguments || {})`,
		`splitToolCallID(message.toolCallId)`,
	}
	for _, want := range required {
		if !strings.Contains(piVoidCodexExtensionSource, want) {
			t.Fatalf("Pi Codex extension source missing %q", want)
		}
	}
}

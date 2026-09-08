package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

type piUISmokeSnapshot struct {
	InactiveTools                []string `json:"inactiveTools"`
	InactiveHandlers             []string `json:"inactiveHandlers"`
	ActiveTools                  []string `json:"activeTools"`
	ActiveHandlers               []string `json:"activeHandlers"`
	EntryRenderers               []string `json:"entryRenderers"`
	CollapsedRead                []string `json:"collapsedRead"`
	ExpandedRead                 []string `json:"expandedRead"`
	HistoryAtText                bool     `json:"historyAtText"`
	HistoryEntries               int      `json:"historyEntries"`
	ReasoningMirrored            bool     `json:"reasoningMirrored"`
	SecretLeaked                 bool     `json:"secretLeaked"`
	WorkingHiddenDuringReasoning bool     `json:"workingHiddenDuringReasoning"`
}

// The pinned Pi must load the real desktop UI, keep it inert outside desktop, and preserve the two ordering regressions found manually.
func TestPiVoidCodeUIExtensionSmoke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the staged manifest this smoke reads is darwin-arm64")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	prerequisites := requireOrSkipPinnedPiSmoke(t, root)
	loader := filepath.Join(filepath.Dir(prerequisites.piEntry), "core", "extensions", "loader.js")
	if _, err := os.Stat(loader); err != nil {
		t.Fatalf("pinned Pi extension loader missing: %v", err)
	}

	work := t.TempDir()
	home := filepath.Join(work, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	extension := filepath.Join(work, "void-code-ui.ts")
	if err := os.WriteFile(extension, []byte(piVoidCodeUIExtensionSource), 0600); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(work, "smoke.mjs")
	if err := os.WriteFile(script, []byte(piUIExtensionSmokeScript), 0600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, prerequisites.node, script, loader, extension)
	command.Dir = work
	command.Env = []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + home,
		"TERM=dumb",
		"PI_PACKAGE_DIR=" + filepath.Dir(filepath.Dir(prerequisites.piEntry)),
	}
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("pinned Pi did not load the desktop UI extension: %v\n%s", err, output)
	}
	var got piUISmokeSnapshot
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode UI smoke output: %v\n%s", err, output)
	}

	if len(got.InactiveTools) != 0 || len(got.InactiveHandlers) != 0 {
		t.Fatalf("desktop UI changed terminal Pi: tools=%v handlers=%v", got.InactiveTools, got.InactiveHandlers)
	}
	sort.Strings(got.ActiveTools)
	wantTools := []string{"bash", "edit", "find", "grep", "ls", "read", "write"}
	if !reflect.DeepEqual(got.ActiveTools, wantTools) {
		t.Fatalf("active compact tools = %v, want %v", got.ActiveTools, wantTools)
	}
	for _, handler := range []string{"message_update", "tool_execution_start", "tool_execution_end", "agent_end"} {
		if !containsString(got.ActiveHandlers, handler) {
			t.Errorf("active desktop UI did not register %s", handler)
		}
	}
	if !containsString(got.EntryRenderers, "compact-tool-history-block") {
		t.Errorf("compact history renderer missing: %v", got.EntryRenderers)
	}
	if len(got.CollapsedRead) != 0 {
		t.Errorf("collapsed read leaked tool chrome/output: %q", got.CollapsedRead)
	}
	if len(got.ExpandedRead) == 0 || !strings.Contains(strings.Join(got.ExpandedRead, "\n"), "secret.txt") {
		t.Errorf("expanded read lost Ctrl+O detail: %q", got.ExpandedRead)
	}
	if !got.HistoryAtText || got.HistoryEntries != 1 {
		t.Errorf("tool history was not inserted exactly once before final text: atText=%v entries=%d", got.HistoryAtText, got.HistoryEntries)
	}
	if got.ReasoningMirrored {
		t.Error("completed reasoning was copied into the live Working line")
	}
	if got.SecretLeaked {
		t.Error("compact status/history leaked a secret from a hidden command")
	}
	if !got.WorkingHiddenDuringReasoning {
		t.Error("Working line stayed visible on top of native reasoning")
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

const piUIExtensionSmokeScript = `
import { pathToFileURL } from "node:url";
const [loaderPath, extensionPath] = process.argv.slice(2);
const { createExtensionRuntime, loadExtensions } = await import(pathToFileURL(loaderPath).href);

async function load(desktop) {
  if (desktop) process.env.VC_DESKTOP_SESSION = "1";
  else delete process.env.VC_DESKTOP_SESSION;
  const timeline = [];
  const runtime = createExtensionRuntime();
  runtime.appendEntry = (customType, data) => timeline.push({ kind: "entry", customType, data });
  const result = await loadExtensions([extensionPath], process.cwd(), undefined, runtime);
  if (result.errors.length > 0) throw new Error(JSON.stringify(result.errors));
  return { extension: result.extensions[0], timeline };
}

const inactive = await load(false);
const active = await load(true);
const ext = active.extension;
const ctx = {
  mode: "tui",
  sessionManager: { getEntries: () => [] },
  ui: {
    setWorkingMessage: (value) => active.timeline.push({ kind: "status", value }),
    setWorkingVisible: (value) => active.timeline.push({ kind: "visible", value }),
    setHiddenThinkingLabel: () => {},
  },
};
const fire = async (name, event = {}) => {
  for (const handler of ext.handlers.get(name) ?? []) await handler(event, ctx);
};

await fire("session_start");
await fire("before_agent_start", { prompt: "Проверь API" });
await fire("turn_start");
await fire("message_update", { assistantMessageEvent: { type: "thinking_start" } });
await fire("message_update", { assistantMessageEvent: { type: "thinking_delta", delta: "PREVIOUS_REASONING" } });
await fire("message_update", { assistantMessageEvent: { type: "thinking_end", content: "PREVIOUS_REASONING" } });
const visibilityAtThinking = active.timeline.filter((item) => item.kind === "visible").map((item) => item.value);
const workingHiddenDuringReasoning = visibilityAtThinking.at(-1) === false;
await fire("tool_execution_start", { toolCallId: "1", toolName: "read", args: { path: "secret.txt" } });
await fire("tool_execution_end", { toolCallId: "1", toolName: "read", result: { content: [{ type: "text", text: "secret" }] }, isError: false });
await fire("tool_execution_start", { toolCallId: "2", toolName: "bash", args: { command: "API_TOKEN=top-secret node ./task.js" } });
await fire("tool_execution_end", { toolCallId: "2", toolName: "bash", result: { content: [{ type: "text", text: "done" }] }, isError: false });
await fire("turn_end", { toolResults: [{}, {}] });
await fire("turn_start");
await fire("message_update", { assistantMessageEvent: { type: "thinking_start" } });
const entriesBeforeText = active.timeline.filter((item) => item.kind === "entry").length;
await fire("message_update", { assistantMessageEvent: { type: "text_start" } });
const entriesAtText = active.timeline.filter((item) => item.kind === "entry").length;
await fire("turn_end", { toolResults: [] });
await fire("agent_end");

const theme = { fg: (_color, value) => value, bold: (value) => value };
const read = ext.tools.get("read").definition;
const collapsedRead = read.renderCall({ path: "secret.txt" }, theme, { expanded: false }).render(80);
const expandedRead = read.renderCall({ path: "secret.txt" }, theme, { expanded: true }).render(80);
const statuses = active.timeline.filter((item) => item.kind === "status").map((item) => String(item.value ?? ""));

console.log(JSON.stringify({
  inactiveTools: [...inactive.extension.tools.keys()],
  inactiveHandlers: [...inactive.extension.handlers.keys()],
  activeTools: [...ext.tools.keys()],
  activeHandlers: [...ext.handlers.keys()],
  entryRenderers: [...ext.entryRenderers.keys()],
  collapsedRead,
  expandedRead,
  historyAtText: entriesBeforeText === 0 && entriesAtText === 1,
  historyEntries: active.timeline.filter((item) => item.kind === "entry").length,
  reasoningMirrored: statuses.some((status) => status.includes("PREVIOUS_REASONING")),
  secretLeaked: JSON.stringify(active.timeline).includes("top-secret"),
  workingHiddenDuringReasoning,
}));
`

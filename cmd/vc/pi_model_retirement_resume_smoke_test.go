// Real pinned-Pi upgrade smoke: a persisted retired model is reconciled by the managed extension on ordinary session startup, without /model or a relay call.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"
)

func TestPiRetiredModelResumeMigratesAndPublishesFreshCatalogSmoke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the trusted bootstrap stub is a POSIX shell script; use the neighboring Windows RPC probe on win32")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	prerequisites := requireOrSkipPinnedPiSmoke(t, root)

	work := t.TempDir()
	home := filepath.Join(work, "home")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	extension := filepath.Join(work, "void-code.ts")
	if err := os.WriteFile(extension, []byte(piVoidCodexExtensionSource), 0600); err != nil {
		t.Fatal(err)
	}

	settingsDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(settingsDir, 0700); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(settingsDir, "settings.json")
	const foreignSettings = `{"defaultProvider":"anthropic","defaultModel":"claude-current","theme":"nord"}`
	if err := os.WriteFile(settingsPath, []byte(foreignSettings), 0600); err != nil {
		t.Fatal(err)
	}
	const bootstrapJSON = `{"version":1,"relayUrl":"https://relay.invalid","authToken":"local-only","providers":[{"kind":"codex","relayProviderId":"codex-local","models":["gpt-6-sol","gpt-6-luna","gpt-6-astra"]}]}`
	const defaultsSnapshot = `{"provider":{"present":true,"value":"anthropic"},"model":{"present":true,"value":"claude-current"}}`
	bootstrap := filepath.Join(work, "bootstrap.sh")
	bootstrapSource := "#!/bin/sh\ncase \"$1\" in\n" +
		"pi-bootstrap) printf '%s' '" + bootstrapJSON + "' ;;\n" +
		"pi-model-default-snapshot) printf '%s' '" + defaultsSnapshot + "' ;;\n" +
		"pi-model-default-restore) cat >/dev/null; printf '%s' '" + foreignSettings + "' >\"$HOME/.pi/agent/settings.json\" ;;\n" +
		"*) exit 1 ;;\nesac\n"
	if err := os.WriteFile(bootstrap, []byte(bootstrapSource), 0700); err != nil {
		t.Fatal(err)
	}

	session := filepath.Join(work, "astra-session.jsonl")
	lines := []any{
		map[string]any{"type": "session", "version": 3, "id": "11111111-1111-4111-8111-111111111111", "timestamp": "2026-09-08T00:00:00.000Z", "cwd": work},
		map[string]any{"type": "message", "id": "message-1", "parentId": nil, "timestamp": "2026-09-08T00:00:01.000Z", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "persisted message"}}, "timestamp": int64(1788825601000)}},
		map[string]any{"type": "message", "id": "assistant-1", "parentId": "message-1", "timestamp": "2026-09-08T00:00:02.000Z", "message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "persisted reply"}}, "provider": "void-codex", "model": "gpt-5.6-luna", "usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}, "stopReason": "stop", "timestamp": int64(1788825602000)}},
	}
	var persisted bytes.Buffer
	for _, line := range lines {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		persisted.Write(encoded)
		persisted.WriteByte('\n')
	}
	if err := os.WriteFile(session, persisted.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, prerequisites.node, prerequisites.piEntry,
		"-e", extension, "--offline", "--mode", "rpc", "--session", session)
	command.Dir = work
	command.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + home, "TERM=dumb", "VC_BOOTSTRAP_EXECUTABLE=" + bootstrap}
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}

	records := make(chan map[string]json.RawMessage)
	readErrors := make(chan error, 1)
	go readStrictJSONL(stdout, records, readErrors)
	for _, request := range []string{
		`{"id":"state","type":"get_state"}` + "\n",
		`{"id":"models","type":"get_available_models"}` + "\n",
	} {
		if _, err := io.WriteString(stdin, request); err != nil {
			t.Fatalf("send RPC command: %v", err)
		}
	}

	responses := map[string]map[string]json.RawMessage{}
	for len(responses) < 2 {
		select {
		case record, ok := <-records:
			if !ok {
				t.Fatalf("pinned Pi closed RPC stdout before both responses; stderr=%s", stderr.String())
			}
			var kind, id string
			_ = json.Unmarshal(record["type"], &kind)
			_ = json.Unmarshal(record["id"], &id)
			if kind == "response" && (id == "state" || id == "models") {
				responses[id] = record
			}
		case err := <-readErrors:
			t.Fatalf("invalid pinned Pi RPC JSONL: %v; stderr=%s", err, stderr.String())
		case <-ctx.Done():
			t.Fatalf("timed out waiting for pinned Pi RPC responses: %v; stderr=%s", ctx.Err(), stderr.String())
		}
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("pinned Pi RPC process failed: %v; stderr=%s", err, stderr.String())
	}

	for id, commandName := range map[string]string{"state": "get_state", "models": "get_available_models"} {
		var success bool
		var gotCommand string
		_ = json.Unmarshal(responses[id]["success"], &success)
		_ = json.Unmarshal(responses[id]["command"], &gotCommand)
		if !success || gotCommand != commandName {
			t.Fatalf("RPC %s response = %s", id, mustJSON(responses[id]))
		}
	}

	var state struct {
		Model *struct {
			Provider string `json:"provider"`
			ID       string `json:"id"`
		} `json:"model"`
		MessageCount int `json:"messageCount"`
	}
	if err := json.Unmarshal(responses["state"]["data"], &state); err != nil {
		t.Fatal(err)
	}
	if state.Model == nil || state.Model.Provider != "void-codex" || state.Model.ID != "gpt-6-luna" {
		t.Fatalf("resumed model = %#v, want automatic void-codex/gpt-6-luna successor (never unknown or null)", state.Model)
	}
	if state.MessageCount != 2 {
		t.Fatalf("resumed message count = %d, want both persisted messages", state.MessageCount)
	}
	preservedSettings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(preservedSettings) != foreignSettings {
		t.Fatalf("session migration overwrote unrelated global defaults: %s", preservedSettings)
	}

	var available struct {
		Models []struct {
			Provider string `json:"provider"`
			ID       string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal(responses["models"]["data"], &available); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, model := range available.Models {
		if strings.HasPrefix(model.Provider, "void-") {
			seen[model.Provider+"/"+model.ID] = true
		}
	}
	for model := range seen {
		if strings.HasPrefix(model, "void-deepseek/") {
			t.Fatalf("retired DeepSeek model is still advertised after extension registration: %s", model)
		}
	}
	want := []string{
		"void-codex/gpt-6-sol",
		"void-codex/gpt-6-luna",
		"void-codex/gpt-6-astra",
	}
	var missing []string
	for _, model := range want {
		if !seen[model] {
			missing = append(missing, model)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("available Void models missing %s; got %s", strings.Join(missing, ", "), mustJSON(seen))
	}
	for model := range seen {
		if strings.Contains(model, "gpt-5.6-") {
			t.Fatalf("retired model remains in fresh catalog: %s", model)
		}
	}
	// Reconcile the already-migrated session a second time. EOF shuts RPC down
	// after startup; no command or /model is involved.
	second := exec.CommandContext(ctx, prerequisites.node, prerequisites.piEntry,
		"-e", extension, "--offline", "--mode", "rpc", "--session", session)
	second.Dir = work
	second.Env = command.Env
	second.Stdin = strings.NewReader("")
	if output, err := second.CombinedOutput(); err != nil {
		t.Fatalf("second reconciliation failed: %v; output=%s", err, output)
	}
	migrated, err := os.ReadFile(session)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(migrated), `"modelId":"gpt-6-luna"`) != 1 {
		t.Fatalf("repeated reconciliation did not leave exactly one successor model_change: %s", migrated)
	}

	// A later assistant selection supersedes an older model_change in Pi's own
	// branch projection. A foreign effective selection must not be migrated.
	foreignSession := filepath.Join(work, "foreign-latest-session.jsonl")
	foreignLines := []any{
		map[string]any{"type": "session", "version": 3, "id": "22222222-2222-4222-8222-222222222222", "timestamp": "2026-09-08T00:00:00.000Z", "cwd": work},
		map[string]any{"type": "model_change", "id": "old-model", "parentId": nil, "timestamp": "2026-09-08T00:00:01.000Z", "provider": "void-codex", "modelId": "gpt-5.6-luna"},
		map[string]any{"type": "message", "id": "foreign-assistant", "parentId": "old-model", "timestamp": "2026-09-08T00:00:02.000Z", "message": map[string]any{"role": "assistant", "content": []any{}, "provider": "anthropic", "model": "claude-current", "usage": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "cost": map[string]any{"input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0, "total": 0}}, "stopReason": "stop", "timestamp": int64(1788825602000)}},
	}
	var foreignPersisted bytes.Buffer
	for _, line := range foreignLines {
		encoded, err := json.Marshal(line)
		if err != nil {
			t.Fatal(err)
		}
		foreignPersisted.Write(encoded)
		foreignPersisted.WriteByte('\n')
	}
	if err := os.WriteFile(foreignSession, foreignPersisted.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	foreign := exec.CommandContext(ctx, prerequisites.node, prerequisites.piEntry,
		"-e", extension, "--offline", "--mode", "rpc", "--session", foreignSession)
	foreign.Dir = work
	foreign.Env = command.Env
	foreign.Stdin = strings.NewReader("")
	if output, err := foreign.CombinedOutput(); err != nil {
		t.Fatalf("foreign-selection reconciliation failed: %v; output=%s", err, output)
	}
	foreignAfter, err := os.ReadFile(foreignSession)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(foreignAfter), `"modelId":"gpt-6-luna"`) {
		t.Fatalf("older VC selection overrode later foreign assistant selection: %s", foreignAfter)
	}
}

func readStrictJSONL(reader io.Reader, records chan<- map[string]json.RawMessage, errors chan<- error) {
	defer close(records)
	buffered := bufio.NewReader(reader)
	for {
		line, err := buffered.ReadString('\n')
		if len(line) > 0 {
			if line[len(line)-1] != '\n' {
				errors <- fmt.Errorf("unterminated final record %q", line)
				return
			}
			line = strings.TrimSuffix(line, "\n")
			if strings.HasSuffix(line, "\r") {
				line = strings.TrimSuffix(line, "\r")
			}
			if line == "" {
				errors <- fmt.Errorf("empty record")
				return
			}
			var record map[string]json.RawMessage
			if decodeErr := json.Unmarshal([]byte(line), &record); decodeErr != nil {
				errors <- fmt.Errorf("decode %q: %w", line, decodeErr)
				return
			}
			records <- record
		}
		if err != nil {
			if err != io.EOF {
				errors <- err
			}
			return
		}
	}
}

func mustJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<json error: %v>", err)
	}
	return string(encoded)
}

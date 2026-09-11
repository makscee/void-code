// rails:pin-on-coverage post-fix release-gap coverage: PR #50 already restored Astra, so no red-first implementation exists; removing Astra from either the local bootstrap catalog or the real embedded extension makes this resumed-session RPC smoke fail before any relay or model call
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

func TestPiAstraResumeRestoresProviderAndModelsSmoke(t *testing.T) {
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

	const bootstrapJSON = `{"version":1,"relayUrl":"https://relay.invalid","authToken":"local-only","providers":[{"kind":"codex","relayProviderId":"codex-local","models":["gpt-5.6-sol","gpt-5.6-terra","gpt-5.6-luna","gpt-6-astra"]},{"kind":"deepseek","relayProviderId":"deepseek-local","models":["deepseek/deepseek-v4-pro","deepseek/deepseek-v4-flash"]}]}`
	bootstrap := filepath.Join(work, "bootstrap.sh")
	if err := os.WriteFile(bootstrap, []byte("#!/bin/sh\n[ \"$1\" = \"pi-bootstrap\" ] || exit 1\nprintf '%s' '"+bootstrapJSON+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}

	session := filepath.Join(work, "astra-session.jsonl")
	lines := []any{
		map[string]any{"type": "session", "version": 3, "id": "11111111-1111-4111-8111-111111111111", "timestamp": "2026-09-08T00:00:00.000Z", "cwd": work},
		map[string]any{"type": "message", "id": "message-1", "parentId": nil, "timestamp": "2026-09-08T00:00:01.000Z", "message": map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "persisted message"}}, "timestamp": int64(1788825601000)}},
		map[string]any{"type": "model_change", "id": "model-1", "parentId": "message-1", "timestamp": "2026-09-08T00:00:02.000Z", "provider": "void-codex", "modelId": "gpt-6-astra"},
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
	if state.Model == nil || state.Model.Provider != "void-codex" || state.Model.ID != "gpt-6-astra" {
		t.Fatalf("resumed model = %#v, want exact void-codex/gpt-6-astra (never unknown or null)", state.Model)
	}
	if state.MessageCount != 1 {
		t.Fatalf("resumed message count = %d, want the persisted message", state.MessageCount)
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
		"void-codex/gpt-6-astra",
		"void-codex/gpt-5.6-sol",
		"void-codex/gpt-5.6-terra",
		"void-codex/gpt-5.6-luna",
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

// rails:pin-on-coverage post-fix release-gap coverage: PR #50 already restored Astra, so no red-first implementation exists; removing Astra from either the local bootstrap catalog or the real embedded extension makes this resumed-session RPC smoke fail before any relay or model call
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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

	readback := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/vc/me" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer smoke" {
			http.Error(w, "fixture bearer mismatch", http.StatusUnauthorized)
			return
		}
		decision := voidCodexSmokeDecision()
		authority, ok := decision["authority"].(map[string]any)
		if !ok {
			http.Error(w, "fixture decision authority is malformed", http.StatusInternalServerError)
			return
		}
		// This fixture represents the server readback for the persisted Astra branch. The
		// controller must apply this authority before Pi exposes or reports the model.
		authority["defaultCodexModelId"] = "gpt-6-astra"
		authority["effectiveCodexModelId"] = "gpt-6-astra"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(decision)
	}))
	defer readback.Close()
	bootstrapJSON := voidCodexSmokeBootstrap(t, readback.URL+"/v1/vc/me", readback.URL)
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
	available, polls, err := waitForPiVoidCodexModels(ctx, stdin, records, readErrors, &stderr, "astra-models")
	if err != nil {
		t.Fatal(err)
	}
	if polls < 1 {
		t.Fatalf("V2 readiness poll count = %d, want at least one actual RPC poll", polls)
	}

	stateResponse, err := func() (map[string]json.RawMessage, error) {
		const id = "astra-state"
		if err := sendPiRPC(stdin, id, "get_state", nil); err != nil {
			return nil, fmt.Errorf("send get_state RPC: %w", err)
		}
		return waitForPiRPCResponse(ctx, records, readErrors, &stderr, id)
	}()
	if err != nil {
		t.Fatal(err)
	}
	if err := piRPCResponseError(stateResponse, "get_state"); err != nil {
		t.Fatal(err)
	}
	if err := stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("pinned Pi RPC process failed after readiness/state readback: %v; stderr=%s", err, stderr.String())
	}

	var state struct {
		Model *struct {
			Provider string `json:"provider"`
			ID       string `json:"id"`
		} `json:"model"`
		MessageCount int `json:"messageCount"`
	}
	if err := json.Unmarshal(stateResponse["data"], &state); err != nil {
		t.Fatal(err)
	}
	if state.Model == nil || state.Model.Provider != "void-codex" || state.Model.ID != "gpt-6-astra" {
		t.Fatalf("resumed model = %#v, want exact void-codex/gpt-6-astra (never unknown or null)", state.Model)
	}
	if state.MessageCount != 1 {
		t.Fatalf("resumed message count = %d, want the persisted message", state.MessageCount)
	}

	seen := available
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

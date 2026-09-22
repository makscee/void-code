package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/makscee/void-code/internal/auth"
)

// This is the settings-only direct-managed startup path: Pi has cached the old
// default before the extension invokes vc pi-bootstrap. The bootstrap response
// must make Luna the deterministic fallback for this launch as it persists the
// same migration for subsequent launches.
func TestPiBootstrapRetiredDefaultSelectsLunaBeforeFallbackSmoke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the built helper executable path is covered by Windows Go tests; pinned RPC qualification runs on POSIX")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	prerequisites := requireOrSkipPinnedPiSmoke(t, root)
	goEnvOutput, err := exec.Command("go", "env", "GOMODCACHE", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	goPaths := strings.Fields(string(goEnvOutput))
	if len(goPaths) != 2 {
		t.Fatalf("unexpected go cache paths: %q", goEnvOutput)
	}
	agentDir := piSettingsSandbox(t)
	settingsPath := writePiSettings(t, agentDir, `{"defaultProvider":"void-codex","defaultModel":"gpt-5.6-luna","theme":"nord"}`, 0600)
	if err := auth.Save("protected-token"); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/vc/providers" || r.Header.Get("Authorization") != "Bearer protected-token" {
			t.Errorf("unexpected provider request %s %q", r.URL.Path, r.Header.Get("Authorization"))
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": []map[string]string{
			{"id": "chatgpt-granted", "name": "ChatGPT", "type": "openai-codex-oauth"},
		}})
	}))
	defer server.Close()

	work := t.TempDir()
	extension := filepath.Join(work, "void-code.ts")
	if err := os.WriteFile(extension, []byte(piVoidCodexExtensionSource), 0600); err != nil {
		t.Fatal(err)
	}
	vc := filepath.Join(work, "vc")
	build := exec.Command("go", "build", "-o", vc, "./cmd/vc")
	build.Dir = root
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOMODCACHE="+goPaths[0], "GOCACHE="+goPaths[1])
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build production vc helper: %v; output=%s", err, output)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, prerequisites.node, prerequisites.piEntry,
		"-e", extension, "--offline", "--mode", "rpc", "--no-session")
	command.Dir = work
	command.Env = []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + os.Getenv("HOME"),
		"USERPROFILE=" + os.Getenv("USERPROFILE"),
		"PI_CODING_AGENT_DIR=" + agentDir,
		"TERM=dumb",
		"VC_AUTH_HOST=" + server.URL,
		"VC_RELAY_HOST=https://relay.invalid",
		"VC_BOOTSTRAP_EXECUTABLE=" + vc,
	}
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
	if _, err := io.WriteString(stdin, `{"id":"state","type":"get_state"}`+"\n"); err != nil {
		t.Fatal(err)
	}

	records := make(chan map[string]json.RawMessage)
	readErrors := make(chan error, 1)
	go readStrictJSONL(stdout, records, readErrors)
	var response map[string]json.RawMessage
	for response == nil {
		select {
		case record, ok := <-records:
			if !ok {
				t.Fatalf("pinned Pi closed before state response; stderr=%s", stderr.String())
			}
			var kind, id string
			_ = json.Unmarshal(record["type"], &kind)
			_ = json.Unmarshal(record["id"], &id)
			if kind == "response" && id == "state" {
				response = record
			}
		case err := <-readErrors:
			t.Fatalf("invalid pinned Pi RPC output: %v; stderr=%s", err, stderr.String())
		case <-ctx.Done():
			t.Fatalf("timed out waiting for state: %v; stderr=%s", ctx.Err(), stderr.String())
		}
	}
	_ = stdin.Close()
	if err := command.Wait(); err != nil {
		t.Fatalf("pinned Pi failed: %v; stderr=%s", err, stderr.String())
	}
	var state struct {
		Model *struct {
			Provider string `json:"provider"`
			ID       string `json:"id"`
		} `json:"model"`
	}
	if err := json.Unmarshal(response["data"], &state); err != nil {
		t.Fatal(err)
	}
	if state.Model == nil || state.Model.Provider != "void-codex" || state.Model.ID != "gpt-6-luna" {
		t.Fatalf("direct managed startup model = %#v, want void-codex/gpt-6-luna; stderr=%s", state.Model, stderr.String())
	}
	settings := readPiSettings(t, settingsPath)
	if settings["defaultModel"] != "gpt-6-luna" || settings["theme"] != "nord" {
		t.Fatalf("persisted bootstrap migration = %#v", settings)
	}
}

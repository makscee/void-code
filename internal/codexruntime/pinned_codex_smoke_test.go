package codexruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The hashes in the managed config are Codex's own sha256 of each hook entry,
// and a wrong one is invisible: Codex keeps the hook "untrusted" and silently
// never runs it. The unit tests pin the spec's values; this test asks the real
// pinned Codex whether it agrees. A bump of the Codex pin or any edit to the
// hook entry makes it red, with the hash Codex now expects in the message.
//
// It needs the pinned binary, so it runs only when asked:
//
//	VC_REQUIRE_PINNED_CODEX_SMOKE=1 VC_CODEX_BIN=/path/to/codex go test ./internal/codexruntime/ -run PinnedCodex
//
// With the switch on and no binary it fails rather than skips: "required"
// means a run that could not check must not read as a run that passed.
func TestPinnedCodexTrustsTheManagedHooks(t *testing.T) {
	if os.Getenv("VC_REQUIRE_PINNED_CODEX_SMOKE") != "1" {
		t.Skip("set VC_REQUIRE_PINNED_CODEX_SMOKE=1 and VC_CODEX_BIN to check the hook hashes against the pinned Codex")
	}
	bin := os.Getenv("VC_CODEX_BIN")
	if bin == "" || !filepath.IsAbs(bin) {
		t.Fatalf("НЕ СМОГ: VC_REQUIRE_PINNED_CODEX_SMOKE=1 but VC_CODEX_BIN=%q is not an absolute path to codex %s", bin, Version)
	}
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("НЕ СМОГ: VC_CODEX_BIN: %v", err)
	}

	// t.TempDir is a symlink path on macOS (/var → /private/var), which is the
	// case the trust keys have to survive: Codex resolves CODEX_HOME first.
	codexHome := filepath.Join(t.TempDir(), "codex-home")
	cwd := t.TempDir()
	if err := WriteConfigFor(codexHome, "https://relay.invalid:443", cwd); err != nil {
		t.Fatalf("WriteConfigFor: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "app-server")
	cmd.Env = smokeEnv(codexHome, t.TempDir())
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s app-server: %v", bin, err)
	}
	defer func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}()

	rpc := &jsonRPC{t: t, in: stdin, out: bufio.NewReader(stdout), stderr: &stderr}
	rpc.call(1, "initialize", map[string]any{"clientInfo": map[string]any{"name": "vc-hook-smoke", "version": "0"}})
	rpc.notify("initialized")
	result := rpc.call(2, "hooks/list", map[string]any{"cwds": []string{cwd}})

	var listed struct {
		Data []struct {
			Hooks []struct {
				Key         string `json:"key"`
				EventName   string `json:"eventName"`
				Command     string `json:"command"`
				CurrentHash string `json:"currentHash"`
				TrustStatus string `json:"trustStatus"`
			} `json:"hooks"`
			Warnings []any `json:"warnings"`
			Errors   []any `json:"errors"`
		} `json:"data"`
	}
	if err := json.Unmarshal(result, &listed); err != nil {
		t.Fatalf("hooks/list result is not the expected shape: %v\n%s", err, result)
	}
	if len(listed.Data) != 1 {
		t.Fatalf("hooks/list returned %d entries for one cwd:\n%s", len(listed.Data), result)
	}
	entry := listed.Data[0]
	if len(entry.Errors) != 0 || len(entry.Warnings) != 0 {
		t.Errorf("Codex reported problems with the managed hooks: errors=%v warnings=%v", entry.Errors, entry.Warnings)
	}
	resolvedHome, err := filepath.EvalSymlinks(codexHome)
	if err != nil {
		t.Fatal(err)
	}
	wantEvent := map[string]string{"sessionStart": "session_start", "userPromptSubmit": "user_prompt_submit", "stop": "stop"}
	seen := map[string]bool{}
	for _, h := range entry.Hooks {
		snake, ok := wantEvent[h.EventName]
		if !ok {
			t.Errorf("unexpected hook %q (%s) in the managed config", h.EventName, h.Key)
			continue
		}
		seen[h.EventName] = true
		if want := filepath.Join(resolvedHome, "config.toml") + ":" + snake + ":0:0"; h.Key != want {
			t.Errorf("%s hook key = %q, want %q", h.EventName, h.Key, want)
		}
		if h.CurrentHash != specHookHashes[snake] {
			t.Errorf("Codex %s hashes the %s hook as %s, the pinned constant for %s is %s — recompute the constants", Version, h.EventName, h.CurrentHash, runtime.GOOS, specHookHashes[snake])
		}
		if h.TrustStatus != "trusted" {
			t.Errorf("%s hook trustStatus = %q, want \"trusted\" — Codex will silently not run it", h.EventName, h.TrustStatus)
		}
	}
	for event := range wantEvent {
		if !seen[event] {
			t.Errorf("Codex does not list a %s hook from the managed config:\n%s", event, result)
		}
	}
}

func smokeEnv(codexHome, home string) []string {
	env := []string{"CODEX_HOME=" + codexHome, "HOME=" + home, "USERPROFILE=" + home, "PATH=" + os.Getenv("PATH")}
	if runtime.GOOS == "windows" {
		for _, k := range []string{"SystemRoot", "TEMP", "TMP", "APPDATA", "LOCALAPPDATA"} {
			if v := os.Getenv(k); v != "" {
				env = append(env, k+"="+v)
			}
		}
	}
	return env
}

type jsonRPC struct {
	t      *testing.T
	in     io.Writer
	out    *bufio.Reader
	stderr *strings.Builder
}

func (r *jsonRPC) send(msg map[string]any) {
	r.t.Helper()
	msg["jsonrpc"] = "2.0"
	data, err := json.Marshal(msg)
	if err != nil {
		r.t.Fatal(err)
	}
	if _, err := fmt.Fprintf(r.in, "%s\n", data); err != nil {
		r.t.Fatalf("write to app-server: %v\nstderr:\n%s", err, r.stderr.String())
	}
}

func (r *jsonRPC) notify(method string) {
	r.t.Helper()
	r.send(map[string]any{"method": method})
}

// call sends a request and returns its result, skipping notifications and
// server requests that arrive in between.
func (r *jsonRPC) call(id int, method string, params any) json.RawMessage {
	r.t.Helper()
	r.send(map[string]any{"id": id, "method": method, "params": params})
	for {
		line, err := r.out.ReadBytes('\n')
		if err != nil {
			r.t.Fatalf("app-server closed before answering %s: %v\nstderr:\n%s", method, err, r.stderr.String())
		}
		var msg struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if err := json.Unmarshal(line, &msg); err != nil {
			continue
		}
		if msg.ID == nil || *msg.ID != id || msg.Method != "" {
			continue
		}
		if len(msg.Error) > 0 && string(msg.Error) != "null" {
			r.t.Fatalf("%s failed: %s", method, msg.Error)
		}
		return msg.Result
	}
}

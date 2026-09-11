package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A source-only checkout must retain the local Terra tombstone even when the pinned runtime smoke skips.
func TestPiVoidCodexNoGrantSourceRegistersLocalTerraTombstone(t *testing.T) {
	code := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(piVoidCodexExtensionSource, "")
	code = regexp.MustCompile(`(?m)^[\t ]*//[^\n]*(?:\n|$)`).ReplaceAllString(code, "")

	required := []struct {
		name    string
		pattern string
	}{
		{
			name:    "Terra model identity",
			pattern: `(?m)^[\t ]*const[\t ]+CODEX_MODEL_ID[\t ]*=[\t ]*"gpt-5\.6-terra"[\t ]*;`,
		},
		{
			name:    "absence-first Codex grant state",
			pattern: `(?m)^[\t ]*let[\t ]+hasCodexGrant[\t ]*=[\t ]*false[\t ]*;`,
		},
		{
			name:    "Codex grant observation",
			pattern: `(?m)^[\t ]*if[\t ]*\([\t ]*provider\.kind[\t ]*===[\t ]*"codex"[\t ]*\)[\t ]*\{[\t\r\n ]*hasCodexGrant[\t ]*=[\t ]*true[\t ]*;`,
		},
		{
			name: "explicit no-grant local Terra registration",
			pattern: `(?m)^[\t ]*if[\t ]*\([\t ]*!hasCodexGrant[\t ]*\)[\t ]*\{[\t\r\n ]*` +
				`registerVoidCodex[\t ]*\([\t ]*pi[\t ]*,[\t ]*bootstrap[\t ]*,[\t ]*\[[\t ]*` +
				`codexModel[\t ]*\([\t ]*CODEX_MODEL_ID[\t ]*,[\t ]*codexName[\t ]*\([\t ]*CODEX_MODEL_ID[\t ]*\)[\t ]*\)[\t ]*` +
				`\][\t ]*\)[\t ]*;[\t\r\n ]*\}`,
		},
	}
	for _, contract := range required {
		if !regexp.MustCompile(contract.pattern).MatchString(code) {
			t.Errorf("embedded Pi extension is missing %s", contract.name)
		}
	}
}

// Without a local tombstone, Pi falls away from the managed Terra model instead of reporting the missing Void grant.
func TestPiVoidCodexNoGrantTombstoneSmoke(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the trusted bootstrap stub is a POSIX shell script; the pinned smoke stages darwin-arm64")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	prerequisites := requireOrSkipPinnedPiSmoke(t, root)

	var upstreamCalls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		upstreamCalls.Add(1)
		http.Error(w, "the no-grant tombstone must fail before transport", http.StatusInternalServerError)
	}))
	defer upstream.Close()

	work := t.TempDir()
	home := filepath.Join(work, "home")
	agentDir := filepath.Join(home, ".pi", "agent")
	if err := os.MkdirAll(agentDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(agentDir, "settings.json"),
		[]byte(`{"defaultProvider":"void-codex","defaultModel":"gpt-5.6-terra"}`),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	extension := filepath.Join(work, "void-code.ts")
	if err := os.WriteFile(extension, []byte(piVoidCodexExtensionSource), 0600); err != nil {
		t.Fatal(err)
	}

	bootstrapPayload, err := json.Marshal(map[string]any{
		"version":   1,
		"relayUrl":  upstream.URL,
		"authToken": "local-bootstrap-token",
		"providers": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	bootstrap := filepath.Join(work, "bootstrap.sh")
	if err := os.WriteFile(bootstrap, []byte("#!/bin/sh\n[ \"$1\" = \"pi-bootstrap\" ] || exit 1\nprintf '%s' \"$VC_EMPTY_BOOTSTRAP\"\n"), 0700); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, prerequisites.node, prerequisites.piEntry,
		"-e", extension, "--offline", "--no-tools", "--no-context-files", "-p", "PING")
	command.Dir = work
	command.Env = []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + home,
		"TERM=dumb",
		"PI_CODING_AGENT_DIR=" + agentDir,
		"VC_BOOTSTRAP_EXECUTABLE=" + bootstrap,
		"VC_EMPTY_BOOTSTRAP=" + string(bootstrapPayload),
	}
	output, runErr := command.CombinedOutput()

	if got := upstreamCalls.Load(); got != 0 {
		t.Fatalf("no-grant prompt made %d loopback upstream call(s), want zero; Pi output:\n%s", got, output)
	}
	const wantError = "Void Codex provider grant is unavailable"
	if !strings.Contains(string(output), wantError) {
		t.Fatalf("managed void-codex/gpt-5.6-terra tombstone was not reached; want %q, run error=%v; Pi output:\n%s", wantError, runErr, output)
	}
	if runErr == nil {
		t.Fatalf("no-grant prompt exited successfully after reporting %q; Pi output:\n%s", wantError, output)
	}
}

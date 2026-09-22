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

// A source-only checkout must not retain the pre-R5 local Terra tombstone path.
func TestPiVoidCodexV1SourceDoesNotRegisterManagedCatalog(t *testing.T) {
	code := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(piVoidCodexExtensionSource, "")
	code = regexp.MustCompile(`(?m)^[\t ]*//[^\n]*(?:\n|$)`).ReplaceAllString(code, "")

	legacyTombstone := regexp.MustCompile(`(?m)^[\t ]*if[\t ]*\([\t ]*!hasCodexGrant[\t ]*\)[\t ]*\{[\t\r\n ]*` +
		`registerVoidCodex[\t ]*\([\t ]*pi[\t ]*,[\t ]*bootstrap[\t ]*,[\t ]*\[[\t ]*` +
		`codexModel[\t ]*\([\t ]*CODEX_MODEL_ID[\t ]*,[\t ]*codexName[\t ]*\([\t ]*CODEX_MODEL_ID[\t ]*\)[\t ]*\)[\t ]*` +
		`\][\t ]*\)[\t ]*;[\t\r\n ]*\}`)
	if legacyTombstone.MatchString(code) {
		t.Fatal("embedded Pi extension still invents a managed Terra catalog when the V1/no-grant path is taken")
	}
}

// A V1 no-grant bootstrap must fail closed before any managed transport is registered.
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
		"-e", extension, "--offline", "--no-tools", "--no-context-files", "--list-models")
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
		t.Fatalf("V1 no-grant model listing made %d loopback upstream call(s), want zero; Pi output:\n%s", got, output)
	}
	if runErr != nil {
		t.Fatalf("V1 no-grant model listing failed: %v; Pi output:\n%s", runErr, output)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "void-codex" {
			t.Fatalf("V1 no-grant bootstrap registered managed model %q; Pi output:\n%s", strings.Join(fields[:2], " "), output)
		}
	}
}

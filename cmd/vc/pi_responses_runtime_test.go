package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Optional local cross-version qualification. Release CI exercises this same runner from
// TestPiVoidCodexExtensionSmoke with its hash-verified pinned runtime, without this override.
func TestPiResponsesRuntimeLocal(t *testing.T) {
	root := os.Getenv("VC_PI_RESPONSES_TEST_ROOT")
	if root == "" {
		t.Skip("set VC_PI_RESPONSES_TEST_ROOT for local cross-version qualification; pinned smoke runs this in CI")
	}
	if !filepath.IsAbs(root) {
		t.Fatal("runtime package root must be absolute")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/vc/me" || r.Header.Get("Authorization") != "Bearer smoke" {
			http.Error(w, "fixture request rejected", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(voidCodexSmokeDecision())
	}))
	defer server.Close()
	runPiResponsesRuntime(t, node, root, voidCodexSmokeBootstrap(t, server.URL+"/v1/vc/me", server.URL))
}

func runPiResponsesRuntime(t *testing.T, node, root, bootstrap string) {
	t.Helper()
	work := t.TempDir()
	extension := filepath.Join(work, "void-code.ts")
	if err := os.WriteFile(extension, []byte(piVoidCodexExtensionSource), 0600); err != nil {
		t.Fatal(err)
	}
	bootstrapFile := filepath.Join(work, "bootstrap.sh")
	if err := os.WriteFile(bootstrapFile, []byte("#!/bin/sh\n[ \"$1\" = \"pi-bootstrap\" ] || exit 1\nprintf '%s' '"+bootstrap+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	bootstrapJSONFile := filepath.Join(work, "bootstrap.json")
	if err := os.WriteFile(bootstrapJSONFile, []byte(bootstrap), 0600); err != nil {
		t.Fatal(err)
	}
	runner, err := filepath.Abs(filepath.Join("testdata", "pi-responses-runtime.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, runner, root, extension, work, bootstrapJSONFile)
	cmd.Dir = work
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + work, "VC_BOOTSTRAP_EXECUTABLE=" + bootstrapFile, "NODE_PATH=" + filepath.Join(work, "global-modules")}
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("offline Responses request/tool stream regression failed: %v", err)
	}
}

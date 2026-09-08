package main

import (
	"context"
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
	runPiResponsesRuntime(t, node, root)
}

func runPiResponsesRuntime(t *testing.T, node, root string) {
	t.Helper()
	work := t.TempDir()
	extension := filepath.Join(work, "void-code.ts")
	if err := os.WriteFile(extension, []byte(piVoidCodexExtensionSource), 0600); err != nil {
		t.Fatal(err)
	}
	bootstrap := filepath.Join(work, "bootstrap.sh")
	if err := os.WriteFile(bootstrap, []byte("#!/bin/sh\n[ \"$1\" = \"pi-bootstrap\" ] || exit 1\nprintf '%s' '"+voidCodexSmokeBootstrap+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	runner, err := filepath.Abs(filepath.Join("testdata", "pi-responses-runtime.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, runner, root, extension, work)
	cmd.Dir = work
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + work, "VC_BOOTSTRAP_EXECUTABLE=" + bootstrap, "NODE_PATH=" + filepath.Join(work, "global-modules")}
	out, err := cmd.CombinedOutput()
	t.Log(string(out))
	if err != nil {
		t.Fatalf("offline Responses request/tool stream regression failed: %v", err)
	}
}

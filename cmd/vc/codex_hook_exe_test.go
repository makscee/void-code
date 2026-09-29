package main

import (
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// Codex runs `"$VC_HOOK_EXE" codex-hook` from the managed config (spec
// 2026-09-29-desktop-codex-chats-design, item 2), in the CLI as in the
// desktop. The variable has to name this very vc — not whatever `vc` is first
// on PATH, and never a value inherited from the parent — or the hook runs a
// different program, or nothing.
func TestCodexLaunchTellsCodexWhereVCIs(t *testing.T) {
	l := prepareCodexLaunch(t, codexGrants, http.StatusOK)
	t.Setenv("VC_HOOK_EXE", "/parent/some-other-vc")
	saveRuntimeKey(t, "codex")
	stubRuntimeMenu(t, false, "", nil)

	if err := runSpawn(nil, nil); err != nil {
		t.Fatalf("runSpawn with runtime=codex: %v", err)
	}
	if l.spawn.calls != 1 {
		t.Fatalf("spawnHarness called %d times, want 1", l.spawn.calls)
	}
	got, n := envCount(l.spawn.env, "VC_HOOK_EXE")
	self := selfExecutable(t)
	if n != 1 || !filepath.IsAbs(got) || !sameFilePath(got, self) {
		t.Fatalf("VC_HOOK_EXE = %q (present %d times), want exactly one: the absolute path of this vc (%s)", got, n, self)
	}
	if strings.Contains(strings.Join(l.spawn.env, "\n"), "/parent/some-other-vc") {
		t.Fatal("the parent's VC_HOOK_EXE reached Codex")
	}
}

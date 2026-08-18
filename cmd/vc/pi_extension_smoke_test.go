package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedExtensionSourceInstallsAtomicallyWithRequiredBootstrap(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	path, err := reconcileManagedPiExtension()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"execFileSync(executable, [\"pi-bootstrap\"]", "VC_BOOTSTRAP_EXECUTABLE", "path.isAbsolute(executable)", "pi.registerProvider(CODEX_PROVIDER_ID", "pi.registerProvider(DEEPSEEK_PROVIDER_ID"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("extension missing %q", want)
		}
	}
}

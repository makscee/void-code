package codexbin

import (
	"strings"
	"testing"
)

func TestResolveNotFound(t *testing.T) {
	t.Setenv("PATH", "")
	if _, err := Resolve(); err == nil {
		t.Fatal("Resolve with empty PATH succeeded, want error")
	}
	if IsInstalled() {
		t.Fatal("IsInstalled with empty PATH = true, want false")
	}
}

func TestInstallInstructionsMentionNpmPackage(t *testing.T) {
	msg := InstallInstructions()
	for _, want := range []string{"npm install -g", "@openai/codex"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("InstallInstructions missing %q: %s", want, msg)
		}
	}
}

func TestMissingMessage(t *testing.T) {
	msg := MissingMessage()
	if !strings.Contains(msg, "codex CLI not found") || !strings.Contains(msg, "@openai/codex") {
		t.Fatalf("MissingMessage = %q", msg)
	}
}

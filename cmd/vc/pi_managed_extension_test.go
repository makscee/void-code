package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestReconcileManagedPiExtensionIsAtomicIdempotentAndOwnershipSafe(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	t.Setenv("VC_PI_MANAGED_PROVIDER", "1")

	path, err := reconcileManagedPiExtension()
	if err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, []byte(piVoidCodexExtensionSource)) {
		t.Fatal("managed extension does not match the versioned source")
	}
	info1, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	path2, err := reconcileManagedPiExtension()
	if err != nil || path2 != path {
		t.Fatalf("idempotent reconcile = %q, %v; want %q", path2, err, path)
	}
	info2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !info1.ModTime().Equal(info2.ModTime()) {
		t.Fatal("current managed extension was rewritten")
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".void-code.ts.tmp-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("atomic temp files left behind: %v, %v", matches, err)
	}

	foreignDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", foreignDir)
	foreignPath := filepath.Join(foreignDir, "extensions", "void-code.ts")
	if err := os.MkdirAll(filepath.Dir(foreignPath), 0700); err != nil {
		t.Fatal(err)
	}
	const foreign = "export default function () {}\n"
	if err := os.WriteFile(foreignPath, []byte(foreign), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileManagedPiExtension(); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("foreign conflict error = %v", err)
	}
	if got, _ := os.ReadFile(foreignPath); string(got) != foreign {
		t.Fatalf("foreign extension overwritten: %q", got)
	}
}

func TestManagedPiExtensionOptOutRemovesOnlyOwnedMaterial(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	t.Setenv("VC_PI_MANAGED_PROVIDER", "1")
	path, err := reconcileManagedPiExtension()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("VC_PI_MANAGED_PROVIDER", "0")
	if _, err := reconcileManagedPiExtension(); err != nil {
		t.Fatalf("remove owned extension: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("owned extension remains: %v", err)
	}

	if err := os.WriteFile(path, []byte("foreign\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileManagedPiExtension(); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("foreign opt-out conflict error = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "foreign\n" {
		t.Fatalf("opt-out removed foreign material: %q", got)
	}
}

// This is the literal-child regression from VV-50: no vc wrapper and no VC_*
// environment reach Pi. The managed extension must bootstrap transiently by
// invoking the non-launching vc command found on PATH.
func TestDirectLiteralPiLoadsManagedVoidProviderWithVCEnvUnset(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture executable is a POSIX script")
	}
	piBin, err := exec.LookPath("pi")
	if err != nil {
		t.Skip("pi CLI not installed")
	}
	dir := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	t.Setenv("VC_PI_MANAGED_PROVIDER", "1")
	path, err := reconcileManagedPiExtension()
	if err != nil {
		t.Fatal(err)
	}
	fakeVC := filepath.Join(binDir, "vc")
	fixture := `#!/bin/sh
if [ "$1" != "pi-bootstrap" ]; then exit 64; fi
printf '%s\n' '{"version":1,"relayUrl":"https://relay.example:443","authToken":"transient-test-token","providers":[{"kind":"codex","relayProviderId":"chatgpt-test","models":["gpt-5.6-sol"]}]}'
`
	if err := os.WriteFile(fakeVC, []byte(fixture), 0700); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(piBin, "--list-models")
	cmd.Env = append(withoutVCEnv(os.Environ()),
		"PI_CODING_AGENT_DIR="+dir,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"PI_OFFLINE=1", "PI_TELEMETRY=0")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("literal pi failed: %v\nstdout:\n%s\nstderr:\n%s\nextension=%s", err, stdout.String(), stderr.String(), path)
	}
	list := stdout.String()
	if !strings.Contains(list, "void-codex") || !strings.Contains(list, "gpt-5.6-sol") {
		t.Fatalf("literal pi did not load exact granted model:\n%s\nstderr:\n%s", list, stderr.String())
	}
	if strings.Contains(list, "gpt-5.6-terra") || strings.Contains(list, "gpt-5.6-luna") {
		t.Fatalf("literal pi exposed ungranted managed models:\n%s", list)
	}
	for _, persistent := range []string{filepath.Join(dir, "settings.json"), path} {
		data, err := os.ReadFile(persistent)
		if err != nil && os.IsNotExist(err) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(data, []byte("transient-test-token")) {
			t.Fatalf("credential persisted in %s", persistent)
		}
	}
}

func withoutVCEnv(env []string) []string {
	out := make([]string, 0, len(env))
	for _, entry := range env {
		if !strings.HasPrefix(entry, "VC_") {
			out = append(out, entry)
		}
	}
	return out
}

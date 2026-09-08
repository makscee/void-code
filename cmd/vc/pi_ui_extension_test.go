package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func piUIExtensionSandbox(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(t.TempDir(), "pi", "agent")
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	t.Setenv("VC_PI_COMPACT_UI", "")
	return dir
}

// A desktop launch must install the product UI without leaving a partly-written extension for Pi to load.
func TestManagedPiUIExtensionInstallsPrivatelyAndIdempotently(t *testing.T) {
	dir := piUIExtensionSandbox(t)

	path, err := reconcileManagedPiUIExtension()
	if err != nil {
		t.Fatalf("reconcileManagedPiUIExtension() error = %v", err)
	}
	if path != filepath.Join(dir, "extensions", "void-code-ui.ts") {
		t.Fatalf("path = %q, want the desktop UI in Pi's standard extension directory", path)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != piVoidCodeUIExtensionSource {
		t.Fatal("installed UI source differs from the source embedded in vc")
	}
	if !strings.HasPrefix(string(first), managedPiUIExtensionMarker) {
		t.Fatal("installed UI has no ownership marker")
	}
	if runtime.GOOS != "windows" {
		if info, statErr := os.Stat(path); statErr != nil {
			t.Fatal(statErr)
		} else if info.Mode().Perm() != 0600 {
			t.Errorf("extension mode = %04o, want 0600", info.Mode().Perm())
		}
	}

	secondPath, err := reconcileManagedPiUIExtension()
	if err != nil {
		t.Fatalf("second reconcileManagedPiUIExtension() error = %v", err)
	}
	second, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Fatal("an idempotent reconciliation changed the extension")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "void-code-ui.ts" {
		t.Fatalf("extension directory contains staging leftovers: %#v", entries)
	}
}

// Product updates may replace only files carrying Void Code's ownership marker.
func TestManagedPiUIExtensionNeverOverwritesForeignFile(t *testing.T) {
	dir := piUIExtensionSandbox(t)
	path := filepath.Join(dir, "extensions", "void-code-ui.ts")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	const foreign = "export default function customUI() {}\n"
	if err := os.WriteFile(path, []byte(foreign), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := reconcileManagedPiUIExtension(); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("foreign extension accepted: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != foreign {
		t.Fatalf("foreign extension was modified:\n%s", after)
	}
}

// Opting out removes our managed file but never deletes or rewrites a user's extension.
func TestManagedPiUIExtensionOptOutRemovesOnlyOwnedFile(t *testing.T) {
	dir := piUIExtensionSandbox(t)
	path, err := reconcileManagedPiUIExtension()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("VC_PI_COMPACT_UI", "false")

	disabledPath, err := reconcileManagedPiUIExtension()
	if err != nil {
		t.Fatalf("disable managed UI: %v", err)
	}
	if disabledPath != "" {
		t.Fatalf("disabled path = %q, want empty", disabledPath)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("owned UI survived opt-out: stat error = %v", err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "extensions"), 0700); err != nil {
		t.Fatal(err)
	}
	const foreign = "// user-owned\n"
	if err := os.WriteFile(path, []byte(foreign), 0600); err != nil {
		t.Fatal(err)
	}
	if disabledPath, err := reconcileManagedPiUIExtension(); err != nil || disabledPath != "" {
		t.Fatalf("opt-out rejected a foreign file it should ignore: path=%q err=%v", disabledPath, err)
	}
	if after, err := os.ReadFile(path); err != nil || string(after) != foreign {
		t.Fatalf("opt-out changed foreign UI: body=%q err=%v", after, err)
	}
}

// A globally discovered managed extension must register nothing in terminal vc sessions.
func TestManagedPiUIExtensionGuardsAllRegistrationsBehindDesktopSession(t *testing.T) {
	guard := strings.Index(piVoidCodeUIExtensionSource, `if (process.env.VC_DESKTOP_SESSION !== "1") return;`)
	firstRegistration := strings.Index(piVoidCodeUIExtensionSource, "pi.register")
	if guard < 0 {
		t.Fatal("managed UI has no desktop-session guard")
	}
	if firstRegistration < 0 || guard > firstRegistration {
		t.Fatal("managed UI registers behavior before checking that Pi belongs to the desktop app")
	}
	for _, forbidden := range []string{"registerProvider", "VC_AUTH_TOKEN", "VC_RELAY_URL"} {
		if strings.Contains(piVoidCodeUIExtensionSource, forbidden) {
			t.Errorf("presentation extension crosses into transport ownership through %q", forbidden)
		}
	}
}

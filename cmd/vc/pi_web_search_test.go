package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Without this upgrade regression, recoverable PDF.js font warnings corrupt Pi's fullscreen TUI.
func TestManagedWebSearchUpgradeSilencesRecoverablePDFWarnings(t *testing.T) {
	home := t.TempDir()
	agentDir := filepath.Join(home, "agent")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("VC_TEST_MANAGED_WEB_NODE_MODULES", managedWebSearchFixture(t))

	path := managedWebSearchPackagePath()
	readability := filepath.Join(path, "node_modules", "@mozilla", "readability")
	if err := os.MkdirAll(readability, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(readability, "package.json"), []byte(`{"name":"@mozilla/readability"}`), 0600); err != nil {
		t.Fatal(err)
	}
	oldManifest := `{"name":"@void-code/pi-web-access","version":"0.13.0-void.1","voidCodeFork":{"patch":"VC-10 managed void-codex seam v1"}}`
	if err := os.WriteFile(filepath.Join(path, "package.json"), []byte(oldManifest), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "pdf-extract.ts"), []byte("getDocumentProxy(new Uint8Array(buffer))"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0700); err != nil {
		t.Fatal(err)
	}
	settings, err := json.Marshal(map[string]any{"packages": []string{path}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), settings, 0600); err != nil {
		t.Fatal(err)
	}

	state, err := reconcileManagedWebSearch(true)
	if err != nil || state != managedWebSearchReady {
		t.Fatalf("upgrade state=%s err=%v", state, err)
	}
	manifest, err := os.ReadFile(filepath.Join(path, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	var installed struct{ Version string }
	if err := json.Unmarshal(manifest, &installed); err != nil {
		t.Fatal(err)
	}
	if installed.Version != "0.13.0-void.2" {
		t.Fatalf("managed web-search version=%q, want upgraded PDF warning fix", installed.Version)
	}
	source, err := os.ReadFile(filepath.Join(path, "pdf-extract.ts"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(source), "getDocumentProxy(new Uint8Array(buffer), { verbosity: 0 })") {
		t.Fatal("managed PDF extraction still allows recoverable PDF.js warnings onto the TUI")
	}
	data, err := os.ReadFile(filepath.Join(agentDir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Packages []string }
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	matches := 0
	for _, item := range got.Packages {
		if item == path {
			matches++
		}
	}
	if matches != 1 {
		t.Fatalf("managed package registrations=%d, want exactly one after upgrade: %v", matches, got.Packages)
	}
}

func TestManagedWebSearchInstallOwnershipAndSetting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
	t.Setenv("VC_TEST_MANAGED_WEB_NODE_MODULES", managedWebSearchFixture(t))
	state, err := reconcileManagedWebSearch(true)
	if err != nil || state != managedWebSearchReady {
		t.Fatalf("install state=%s err=%v", state, err)
	}
	path := managedWebSearchPackagePath()
	current, foreign, err := inspectManagedWebSearchPackage(path)
	if err != nil || !current || foreign {
		t.Fatalf("ownership current=%v foreign=%v err=%v", current, foreign, err)
	}
	enabled, err := inspectManagedPackageSetting(path)
	if err != nil || !enabled {
		t.Fatalf("setting enabled=%v err=%v", enabled, err)
	}
	if err := os.WriteFile(filepath.Join(path, "package.json"), []byte(`{"name":"foreign"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := reconcileManagedWebSearch(true); err == nil {
		t.Fatal("foreign managed path overwritten")
	}
}

// managedWebSearchFixture writes the smallest node_modules tree that counts as
// installed, and returns the directory the vctestfixture seam copies from.
//
// The tree is one file because one file is all that is inspected:
// inspectManagedWebSearchPackage stats node_modules/@mozilla/readability/package.json
// and asks nothing about its contents. Every assertion above is about ownership,
// the settings key and the foreign-path guard; none of them reads a dependency.
//
// Written with os.WriteFile rather than fetched, deliberately. A fixture that
// npm-installs during setup does not remove the registry from the run, it only
// moves it earlier, and this fixture exists to remove it.
//
// The hole this leaves, so the next reader does not have to find it: in a binary
// built with -tags vctestfixture, `npm ci` runs nowhere in the suite. Green here
// means ownership, the setting and the guard hold. It is not evidence that the
// real installation works — that path is covered by no test at all after the
// switch, and the seam is the reason.
//
// In an untagged binary the environment variable is read by nobody and the
// production `npm ci` runs as before, so this setup is inert there.
func managedWebSearchFixture(t *testing.T) string {
	t.Helper()
	fixture := t.TempDir()
	readability := filepath.Join(fixture, "@mozilla", "readability")
	if err := os.MkdirAll(readability, 0700); err != nil {
		t.Fatalf("stage managed web-search fixture: %v", err)
	}
	manifest := []byte(`{"name":"@mozilla/readability","version":"0.6.0"}`)
	if err := os.WriteFile(filepath.Join(readability, "package.json"), manifest, 0600); err != nil {
		t.Fatalf("stage managed web-search fixture: %v", err)
	}
	return fixture
}

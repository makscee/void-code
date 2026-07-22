package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedWebSearchReconcileInstallNoopOptOutAndPreserveSettings(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	t.Setenv("VC_PI_MANAGED_WEB_SEARCH", "1")
	settings := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"theme":"solarized","packages":["npm:foreign@1.0.0"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	installs := 0
	old := installManagedWebSearchDependencies
	installManagedWebSearchDependencies = func(dir string) error {
		installs++
		dep := filepath.Join(dir, "node_modules", "@mozilla", "readability")
		if err := os.MkdirAll(dep, 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dep, "package.json"), []byte("{}"), 0600)
	}
	defer func() { installManagedWebSearchDependencies = old }()

	state, err := reconcileManagedWebSearch(true)
	if err != nil || state != managedWebSearchReady {
		t.Fatalf("install = %q, %v", state, err)
	}
	if installs != 1 {
		t.Fatalf("installs = %d", installs)
	}
	first, _ := os.ReadFile(settings)
	state, err = reconcileManagedWebSearch(true)
	if err != nil || state != managedWebSearchReady || installs != 1 {
		t.Fatalf("noop = %q, %v installs=%d", state, err, installs)
	}
	second, _ := os.ReadFile(settings)
	if string(first) != string(second) {
		t.Fatal("no-op rewrote settings")
	}
	var value map[string]any
	if err := json.Unmarshal(second, &value); err != nil {
		t.Fatal(err)
	}
	if value["theme"] != "solarized" {
		t.Fatalf("unrelated setting lost: %#v", value)
	}
	if strings.Contains(string(second), "web-search.json") || strings.Contains(string(second), "API_KEY") {
		t.Fatalf("forbidden config write: %s", second)
	}

	t.Setenv("VC_PI_MANAGED_WEB_SEARCH", "0")
	state, err = reconcileManagedWebSearch(true)
	if err != nil || state != managedWebSearchUnavailable {
		t.Fatalf("optout = %q, %v", state, err)
	}
	data, _ := os.ReadFile(settings)
	if strings.Contains(string(data), managedWebSearchPackageName) || !strings.Contains(string(data), "npm:foreign@1.0.0") {
		t.Fatalf("unsafe removal: %s", data)
	}
}

func TestManagedWebSearchUnavailableAndBrokenOwnership(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	t.Setenv("VC_PI_MANAGED_WEB_SEARCH", "1")
	state, err := reconcileManagedWebSearch(false)
	if err != nil || state != managedWebSearchUnavailable {
		t.Fatalf("ineligible = %q, %v", state, err)
	}
	path := managedWebSearchPackagePath()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "foreign"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	state, err = reconcileManagedWebSearch(true)
	if err == nil || state != managedWebSearchBroken || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("foreign = %q, %v", state, err)
	}
	if _, err := os.Stat(filepath.Join(path, "foreign")); err != nil {
		t.Fatal("foreign material removed")
	}
}

func TestManagedForkContractAndInstruction(t *testing.T) {
	for _, needle := range []string{
		`"gpt-5.6-terra", "gpt-5.6-sol", "gpt-5.6-luna"`,
		`ctx.modelRegistry.find(candidate.provider, modelId)`,
		`auth.provider === "void-codex"`,
		`isManagedVoidSearchAvailable`,
	} {
		if !strings.Contains(piWebAccessOpenAISource+piWebAccessRoutingSource, needle) {
			t.Fatalf("fork missing %q", needle)
		}
	}
	for _, needle := range []string{"web_search", "fetch_content", "multiple queries", "primary sources", "cite links"} {
		if !strings.Contains(piVoidCodexExtensionSource, needle) {
			t.Fatalf("instruction missing %q", needle)
		}
	}
	if !strings.Contains(piVoidCodexExtensionSource, `headers: { "x-void-provider": provider.relayProviderId }`) {
		t.Fatal("managed provider does not publish opaque grant header")
	}
}

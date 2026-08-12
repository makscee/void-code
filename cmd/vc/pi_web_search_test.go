package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/makscee/void-code/internal/provider"
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
	path := managedWebSearchPackagePath()
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(fmt.Sprintf(`{"packages":[%q]}`, path)), 0600); err != nil {
		t.Fatal(err)
	}
	state, err := reconcileManagedWebSearch(false)
	if err != nil || state != managedWebSearchUnavailable {
		t.Fatalf("ineligible = %q, %v", state, err)
	}
	if registered, err := inspectManagedPackageSetting(path); err != nil || registered {
		t.Fatalf("ineligible package remains registered=%v err=%v", registered, err)
	}
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

func TestManagedWebSearchUpdatePublishFailurePreservesCurrentPackage(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	path := managedWebSearchPackagePath()
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	oldPackage := `{"name":"@void-code/pi-web-access","version":"0.12.0-void.1","voidCodeFork":{"patch":"VC-10 managed void-codex seam v1"}}`
	if err := os.WriteFile(filepath.Join(path, "package.json"), []byte(oldPackage), 0600); err != nil {
		t.Fatal(err)
	}
	oldInstall := installManagedWebSearchDependencies
	installManagedWebSearchDependencies = func(dir string) error {
		dep := filepath.Join(dir, "node_modules", "@mozilla", "readability")
		if err := os.MkdirAll(dep, 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dep, "package.json"), []byte("{}"), 0600)
	}
	defer func() { installManagedWebSearchDependencies = oldInstall }()
	oldRename := renameManagedWebSearchPath
	renameManagedWebSearchPath = func(oldPath, newPath string) error {
		if strings.Contains(filepath.Base(oldPath), ".pi-web-access-stage-") && newPath == path {
			return errors.New("injected publish failure")
		}
		return os.Rename(oldPath, newPath)
	}
	defer func() { renameManagedWebSearchPath = oldRename }()

	state, err := reconcileManagedWebSearch(true)
	if err == nil || state != managedWebSearchBroken || !strings.Contains(err.Error(), "injected publish failure") {
		t.Fatalf("update = %q, %v", state, err)
	}
	got, readErr := os.ReadFile(filepath.Join(path, "package.json"))
	if readErr != nil {
		t.Fatalf("prior package missing after failed publish: %v", readErr)
	}
	if string(got) != oldPackage {
		t.Fatalf("prior package changed after failed publish: %s", got)
	}
}

func TestManagedWebSearchDoctorRequiresExactSettingsRegistration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(dir, ".pi", "agent"))
	t.Setenv("VC_PI_MANAGED_WEB_SEARCH", "1")
	if err := provider.Save(provider.Provider{Kind: provider.RelayProvider, ID: "chatgpt-smoke"}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveLabel("ChatGPT relay"); err != nil {
		t.Fatal(err)
	}
	oldInstall := installManagedWebSearchDependencies
	installManagedWebSearchDependencies = func(dir string) error {
		dep := filepath.Join(dir, "node_modules", "@mozilla", "readability")
		if err := os.MkdirAll(dep, 0700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dep, "package.json"), []byte("{}"), 0600)
	}
	defer func() { installManagedWebSearchDependencies = oldInstall }()
	if state, err := reconcileManagedWebSearch(true); err != nil || state != managedWebSearchReady {
		t.Fatalf("install = %q, %v", state, err)
	}
	if err := os.WriteFile(piSettingsPath(), []byte(`{"packages":`), 0600); err != nil {
		t.Fatal(err)
	}
	got := checkManagedWebSearch()
	if got.status != "✗" || !strings.Contains(got.message, "broken") || got.fix == nil {
		t.Fatalf("eligible malformed settings doctor = %#v", got)
	}

	if err := provider.Save(provider.Provider{Kind: provider.Relay}); err != nil {
		t.Fatal(err)
	}
	if err := provider.SaveLabel("DeepSeek relay"); err != nil {
		t.Fatal(err)
	}
	got = checkManagedWebSearch()
	if got.status != "✗" || got.fix != nil || len(got.guidance) != 0 {
		t.Fatalf("ineligible malformed settings doctor = %#v", got)
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

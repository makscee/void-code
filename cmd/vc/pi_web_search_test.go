package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestManagedWebSearchInstallOwnershipAndSetting(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PI_CODING_AGENT_DIR", filepath.Join(home, "agent"))
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

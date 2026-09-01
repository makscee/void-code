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

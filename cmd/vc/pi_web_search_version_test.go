package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"testing"
)

// managedWebSearchForkDigests pins each released fork version to the exact
// embedded content it shipped with. vc reinstalls the fork only when the
// version changes, so new content under an old version never reaches users
// who already have that version installed (void-board#138).
//
// When you change anything under embed/pi-web-access-0.13.0: bump the version
// in its package.json, package-lock.json and managedWebSearchPackageVersion,
// then add the new version here with the digest this test prints.
var managedWebSearchForkDigests = map[string]string{
	"0.13.0-void.2": "904baf79b2c51ee8a871b4e269e1f11d530df49d4eb931e8d404efb8a85522cc", // v0.2.59; v0.2.54 shipped other content under this version
	"0.13.0-void.3": "0a7dc700382d4a86e59b57aebcc1fa87c3e3608487e30437cd28e7bf4ff23082", // GPT-6 model list from #75 (void-board#138)
}

func TestManagedWebSearchForkContentMatchesVersion(t *testing.T) {
	var manifest struct{ Version string }
	data, err := piWebAccessFork.ReadFile("embed/pi-web-access-0.13.0/package.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != managedWebSearchPackageVersion {
		t.Fatalf("embedded fork package.json version=%q, managedWebSearchPackageVersion=%q; keep them equal", manifest.Version, managedWebSearchPackageVersion)
	}
	var lock struct {
		Version  string
		Packages map[string]struct{ Version string }
	}
	data, err = piWebAccessFork.ReadFile("embed/pi-web-access-0.13.0/package-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &lock); err != nil {
		t.Fatal(err)
	}
	if lock.Version != manifest.Version || lock.Packages[""].Version != manifest.Version {
		t.Fatalf("embedded fork package-lock.json version=%q/%q, package.json=%q; keep them equal", lock.Version, lock.Packages[""].Version, manifest.Version)
	}

	got := managedWebSearchForkDigest(t)
	want, known := managedWebSearchForkDigests[manifest.Version]
	if !known {
		t.Fatalf("fork version %s has no pinned digest; add %q: %q to managedWebSearchForkDigests", manifest.Version, manifest.Version, got)
	}
	if got != want {
		t.Fatalf("embedded fork content changed but its version is still %s, so vc update would keep the old copy.\nBump the version (package.json, package-lock.json, managedWebSearchPackageVersion) and pin the new one with digest %q.\nIf this version is not released yet, update its digest instead.", manifest.Version, got)
	}
}

// managedWebSearchForkDigest hashes every embedded fork file by path and content.
func managedWebSearchForkDigest(t *testing.T) string {
	t.Helper()
	hash := sha256.New()
	err := fs.WalkDir(piWebAccessFork, "embed/pi-web-access-0.13.0", func(name string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		data, err := piWebAccessFork.ReadFile(name)
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%s\x00%d\x00", name, len(data))
		hash.Write(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

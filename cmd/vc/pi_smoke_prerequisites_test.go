package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const requirePinnedPiSmokeEnv = "VC_REQUIRE_PINNED_PI_SMOKE"

const pinnedPiSmokeSkipPrefix = "pinned Pi smoke prerequisites unavailable or stale"

type desktopResourcePins struct {
	Node struct {
		Version          string `json:"version"`
		ExecutableSHA256 string `json:"executableSha256"`
	} `json:"node"`
	Pi struct {
		Version    string `json:"version"`
		TreeSHA256 string `json:"treeSha256"`
	} `json:"pi"`
}

type desktopStagedManifest struct {
	Schema   int    `json:"schema"`
	Platform string `json:"platform"`
	Node     struct {
		Version string `json:"version"`
		Path    string `json:"path"`
		SHA256  string `json:"sha256"`
	} `json:"node"`
	Pi struct {
		Version    string `json:"version"`
		Entry      string `json:"entry"`
		TreeSHA256 string `json:"treeSha256"`
	} `json:"pi"`
}

type pinnedPiPrerequisites struct {
	node    string
	piEntry string
	npm     string
}

func pinnedPiSmokePrerequisites(root string) (pinnedPiPrerequisites, error) {
	readJSON := func(name string, target any) error {
		data, err := os.ReadFile(name)
		if err != nil {
			return fmt.Errorf("read %s: %w", filepath.Base(name), err)
		}
		if err := json.Unmarshal(data, target); err != nil {
			return fmt.Errorf("parse %s: %w", filepath.Base(name), err)
		}
		return nil
	}
	var pins desktopResourcePins
	if err := readJSON(filepath.Join(root, "desktop", "scripts", "resource-pins.json"), &pins); err != nil {
		return pinnedPiPrerequisites{}, err
	}
	staged := filepath.Join(root, "desktop", "resources", "staged")
	var manifest desktopStagedManifest
	if err := readJSON(filepath.Join(staged, "manifest.json"), &manifest); err != nil {
		return pinnedPiPrerequisites{}, err
	}
	if manifest.Schema != 1 || manifest.Platform != "darwin-arm64" {
		return pinnedPiPrerequisites{}, fmt.Errorf("staged manifest identity is %d/%q, want 1/darwin-arm64", manifest.Schema, manifest.Platform)
	}
	if manifest.Node.Version != pins.Node.Version || manifest.Node.SHA256 != pins.Node.ExecutableSHA256 {
		return pinnedPiPrerequisites{}, fmt.Errorf("staged Node pin differs from resource-pins.json")
	}
	if manifest.Pi.Version != pins.Pi.Version || manifest.Pi.TreeSHA256 != pins.Pi.TreeSHA256 {
		return pinnedPiPrerequisites{}, fmt.Errorf("staged Pi pin differs from resource-pins.json")
	}
	cleanPath := func(name, relative string) (string, error) {
		if relative == "" || filepath.IsAbs(relative) || strings.HasPrefix(filepath.Clean(relative), ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("unsafe staged %s path %q", name, relative)
		}
		path := filepath.Join(staged, filepath.FromSlash(relative))
		if _, err := os.Stat(path); err != nil {
			return "", fmt.Errorf("staged %s missing: %w", name, err)
		}
		return path, nil
	}
	node, err := cleanPath("Node", manifest.Node.Path)
	if err != nil {
		return pinnedPiPrerequisites{}, err
	}
	piEntry, err := cleanPath("Pi entry", manifest.Pi.Entry)
	if err != nil {
		return pinnedPiPrerequisites{}, err
	}
	nodeBytes, err := os.ReadFile(node)
	if err != nil {
		return pinnedPiPrerequisites{}, fmt.Errorf("read staged Node: %w", err)
	}
	actualNodeHash := sha256.Sum256(nodeBytes)
	if hex.EncodeToString(actualNodeHash[:]) != pins.Node.ExecutableSHA256 {
		return pinnedPiPrerequisites{}, fmt.Errorf("staged Node executable hash differs from resource-pins.json")
	}
	out, err := exec.Command(node, piEntry, "--version").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != pins.Pi.Version {
		return pinnedPiPrerequisites{}, fmt.Errorf("staged Pi version is %q (err=%v), want %s", strings.TrimSpace(string(out)), err, pins.Pi.Version)
	}
	npm := filepath.Join(filepath.Dir(node), "npm")
	if _, err := os.Stat(npm); err != nil {
		return pinnedPiPrerequisites{}, fmt.Errorf("staged npm missing: %w", err)
	}
	return pinnedPiPrerequisites{node: node, piEntry: piEntry, npm: npm}, nil
}

func requireOrSkipPinnedPiSmoke(t *testing.T, root string) pinnedPiPrerequisites {
	t.Helper()
	prerequisites, err := pinnedPiSmokePrerequisites(root)
	if err == nil {
		return prerequisites
	}
	reason := pinnedPiSmokeSkipPrefix + ": " + err.Error()
	if os.Getenv(requirePinnedPiSmokeEnv) == "1" {
		t.Fatal(reason)
	}
	t.Skip(reason + "; provision exact desktop resources or set " + requirePinnedPiSmokeEnv + "=1 to require them")
	return pinnedPiPrerequisites{}
}

func TestPinnedPiSmokePrerequisitePolicyRequiredModeFails(t *testing.T) {
	if root := os.Getenv("VC_PINNED_PI_POLICY_HELPER_ROOT"); root != "" {
		requireOrSkipPinnedPiSmoke(t, root)
		return
	}
	for _, test := range []struct {
		name    string
		require string
		stale   bool
		wantOK  bool
		want    string
	}{
		{name: "absent default skips", wantOK: true, want: "SKIP"},
		{name: "absent required fails", require: "1", want: pinnedPiSmokeSkipPrefix},
		{name: "stale default skips", stale: true, wantOK: true, want: "SKIP"},
		{name: "stale required fails", require: "1", stale: true, want: pinnedPiSmokeSkipPrefix},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			if test.stale {
				writeStalePinnedPiFixture(t, root)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestPinnedPiSmokePrerequisitePolicyRequiredModeFails$", "-test.v")
			cmd.Env = append(os.Environ(), "VC_PINNED_PI_POLICY_HELPER_ROOT="+root, requirePinnedPiSmokeEnv+"="+test.require)
			out, err := cmd.CombinedOutput()
			if (err == nil) != test.wantOK || !strings.Contains(string(out), test.want) {
				t.Fatalf("helper err=%v output=%s", err, out)
			}
		})
	}
}

func TestPinnedPiSmokePrerequisitePolicyAbsentAndStale(t *testing.T) {
	root := t.TempDir()
	_, err := pinnedPiSmokePrerequisites(root)
	if err == nil || !strings.Contains(err.Error(), "resource-pins.json") {
		t.Fatalf("absent prerequisites error = %v", err)
	}

	writeStalePinnedPiFixture(t, root)
	_, err = pinnedPiSmokePrerequisites(root)
	if err == nil || !strings.Contains(err.Error(), "staged Pi pin differs") {
		t.Fatalf("stale prerequisites error = %v", err)
	}
}

func writeStalePinnedPiFixture(t *testing.T, root string) {
	t.Helper()
	scripts := filepath.Join(root, "desktop", "scripts")
	staged := filepath.Join(root, "desktop", "resources", "staged")
	if err := os.MkdirAll(scripts, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(staged, 0700); err != nil {
		t.Fatal(err)
	}
	pins := `{"node":{"version":"v22.23.1","executableSha256":"` + strings.Repeat("a", 64) + `"},"pi":{"version":"0.84.1","treeSha256":"` + strings.Repeat("b", 64) + `"}}`
	manifest := `{"schema":1,"platform":"darwin-arm64","node":{"version":"v22.23.1","path":"node/bin/node","sha256":"` + strings.Repeat("a", 64) + `"},"pi":{"version":"0.84.0","entry":"pi/cli.js","treeSha256":"` + strings.Repeat("b", 64) + `"}}`
	if err := os.WriteFile(filepath.Join(scripts, "resource-pins.json"), []byte(pins), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staged, "manifest.json"), []byte(manifest), 0600); err != nil {
		t.Fatal(err)
	}
}

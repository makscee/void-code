package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

// The pair vc writes into Pi's settings so a fresh user lands on Terra instead
// of whatever provider relay happens to register first.
const (
	wantPiDefaultProvider = "void-codex"
	wantPiDefaultModel    = "gpt-5.6-terra"
)

// piSettingsSandbox isolates both seams that can resolve to a real home:
// PI_CODING_AGENT_DIR (checked first by piSettingsPath) and HOME/USERPROFILE.
// The returned directory does not exist yet — creating it is the callee's job.
func piSettingsSandbox(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	dir := filepath.Join(t.TempDir(), "pi", "agent")
	t.Setenv("PI_CODING_AGENT_DIR", dir)
	return dir
}

func writePiSettings(t *testing.T, dir, body string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func readPiSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	// UseNumber so an integer beyond float64 precision is compared as the digits
	// that were on disk, not as whatever a float round-trip turned it into.
	dec.UseNumber()
	var got map[string]any
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("settings at %s is not valid JSON (%v): %s", path, err, data)
	}
	return got
}

func assertDefaultsWritten(t *testing.T, settings map[string]any) {
	t.Helper()
	if got := settings["defaultProvider"]; got != wantPiDefaultProvider {
		t.Errorf("defaultProvider = %#v, want %q", got, wantPiDefaultProvider)
	}
	if got := settings["defaultModel"]; got != wantPiDefaultModel {
		t.Errorf("defaultModel = %#v, want %q", got, wantPiDefaultModel)
	}
}

// Acceptance criterion 1: no file → created with both keys, directory created.
func TestEnsurePiDefaultModelCreatesMissingFile(t *testing.T) {
	dir := piSettingsSandbox(t)

	if err := ensurePiDefaultModel(); err != nil {
		t.Fatalf("ensurePiDefaultModel() error = %v", err)
	}

	assertDefaultsWritten(t, readPiSettings(t, filepath.Join(dir, "settings.json")))
}

// Acceptance criterion 2: keys absent → keys added, every other field preserved,
// including fields vc knows nothing about and nested objects.
func TestEnsurePiDefaultModelPreservesUnknownFields(t *testing.T) {
	dir := piSettingsSandbox(t)
	const body = `{
  "theme": "nord",
  "maxTokens": 1000000,
  "lastSessionSeq": 9007199254740993,
  "telemetry": false,
  "editor": {"vimMode": true, "tabWidth": 2, "rulers": [80, 120]}
}`
	path := writePiSettings(t, dir, body, 0600)

	if err := ensurePiDefaultModel(); err != nil {
		t.Fatalf("ensurePiDefaultModel() error = %v", err)
	}

	got := readPiSettings(t, path)
	assertDefaultsWritten(t, got)
	want := readPiSettings(t, writePiSettings(t, t.TempDir(), body, 0600))
	for key, wantValue := range want {
		gotValue, ok := got[key]
		if !ok {
			t.Errorf("field %q dropped", key)
			continue
		}
		if !reflect.DeepEqual(gotValue, wantValue) {
			t.Errorf("field %q = %#v, want %#v", key, gotValue, wantValue)
		}
	}
	if len(got) != len(want)+2 {
		t.Errorf("settings gained unexpected keys: %#v", got)
	}
}

// Acceptance criterion 3: defaultModel already set → file is not touched at all,
// whatever the value and whoever set it.
func TestEnsurePiDefaultModelLeavesUserModelAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{name: "another void model", body: `{"defaultProvider":"void-codex","defaultModel":"gpt-5.6-luna"}`},
		{name: "foreign provider", body: `{"defaultProvider":"anthropic","defaultModel":"claude-opus-5"}`},
		{name: "model without provider", body: `{"defaultModel":"gpt-5.6-luna","theme":"nord"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := piSettingsSandbox(t)
			path := writePiSettings(t, dir, tc.body, 0600)

			if err := ensurePiDefaultModel(); err != nil {
				t.Fatalf("ensurePiDefaultModel() error = %v", err)
			}

			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != tc.body {
				t.Errorf("file rewritten\n got: %s\nwant: %s", after, tc.body)
			}
		})
	}
}

// Acceptance criterion 4 of the default-model spec, as corrected by criterion 9
// of the single-owner spec: with a provider already chosen and no model, only
// the missing half is added and everything else stays put.
//
// This test used to make that point with defaultProvider "void-deepseek" and
// required gpt-5.6-terra to be appended next to it — a pair no provider serves,
// because the extension's deepseek branch filters that model out
// (pi_extension.go:59). The rule the test was written for is intact; the one
// case it stated the rule with was the case where the rule does not hold. The
// provider the seed owns is the honest way to state it, and the foreign-provider
// case now lives in TestEnsurePiDefaultModelDoesNotInventAProviderModelPair.
func TestEnsurePiDefaultModelAddsOnlyModelWhenItsOwnProviderIsSet(t *testing.T) {
	dir := piSettingsSandbox(t)
	path := writePiSettings(t, dir, `{"defaultProvider":"`+wantPiDefaultProvider+`","theme":"nord"}`, 0600)

	if err := ensurePiDefaultModel(); err != nil {
		t.Fatalf("ensurePiDefaultModel() error = %v", err)
	}

	got := readPiSettings(t, path)
	if got["defaultProvider"] != wantPiDefaultProvider {
		t.Errorf("defaultProvider = %#v, want %q (user's choice must survive)", got["defaultProvider"], wantPiDefaultProvider)
	}
	if got["defaultModel"] != wantPiDefaultModel {
		t.Errorf("defaultModel = %#v, want %q", got["defaultModel"], wantPiDefaultModel)
	}
	if got["theme"] != "nord" {
		t.Errorf("theme = %#v, want %q", got["theme"], "nord")
	}
}

// Acceptance criterion 5: unreadable/broken JSON → nothing written, no panic,
// and the caller learns about it through a non-nil error (which the spawn path
// downgrades to a stderr warning rather than a failed launch).
func TestEnsurePiDefaultModelRefusesBrokenJSON(t *testing.T) {
	dir := piSettingsSandbox(t)
	const body = `{"defaultProvider": "void-codex",`
	path := writePiSettings(t, dir, body, 0600)

	err := ensurePiDefaultModel()
	if err == nil {
		t.Fatal("ensurePiDefaultModel() error = nil, want an error for malformed settings")
	}

	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != body {
		t.Errorf("malformed file was rewritten\n got: %s\nwant: %s", after, body)
	}
}

// Acceptance criterion 6: PI_CODING_AGENT_DIR wins over the home directory.
func TestEnsurePiDefaultModelRespectsPiCodingAgentDir(t *testing.T) {
	dir := piSettingsSandbox(t)

	if err := ensurePiDefaultModel(); err != nil {
		t.Fatalf("ensurePiDefaultModel() error = %v", err)
	}

	assertDefaultsWritten(t, readPiSettings(t, filepath.Join(dir, "settings.json")))
	home := os.Getenv("HOME")
	if _, err := os.Stat(filepath.Join(home, ".pi", "agent", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("wrote under HOME despite PI_CODING_AGENT_DIR: stat err = %v", err)
	}
}

// Acceptance criterion 7, first half: the file vc creates is not readable by
// anyone else, and the atomic staging file does not survive the call.
//
// The 0600 assertion runs on POSIX only. Windows has no permission bits for Go
// to report — os.Stat gives 0666 for any ordinary file and 0444 for a read-only
// one, os.Chmod moves only the read-only flag, and the actual access control
// lives in ACLs that os.FileMode cannot express. Demanding 0600 there demands a
// number the platform will never produce, and the test fails on correct code.
//
// Plainly, so a green Windows run is not misread: on Windows this subtest does
// NOT check the privacy of the created file, and nothing here does. A settings
// file next to a token deserves that check, and it is verified on POSIX only.
// What remains on Windows is narrow but real — the file must come out writable,
// because a seed that created it read-only would leave Pi unable to save the
// user's next model change.
//
// The staging-leftover assertion is the atomicity half and runs on both: it
// needs no permission bits.
func TestEnsurePiDefaultModelCreatesPrivateFile(t *testing.T) {
	dir := piSettingsSandbox(t)

	if err := ensurePiDefaultModel(); err != nil {
		t.Fatalf("ensurePiDefaultModel() error = %v", err)
	}

	got := statPerm(t, filepath.Join(dir, "settings.json"))
	if runtime.GOOS == "windows" {
		if got&0200 == 0 {
			t.Errorf("settings.json mode = %04o, want a writable file — Pi has to be able to save the user's next model choice", got)
		}
	} else if got != 0600 {
		t.Errorf("settings.json mode = %04o, want 0600 — it sits beside the token and must not be readable by others", got)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "settings.json" {
			t.Errorf("staging leftover in %s: %s", dir, e.Name())
		}
	}
}

// Acceptance criterion 7, second half — an existing file keeps its own mode —
// is not tested here. TestPiSettingsWritersPreserveExistingFileMode in
// pi_settings_contract_test.go makes exactly that claim on exactly this fixture,
// portably, and for both writers of settings.json rather than the seed alone. A
// second copy of it in this file would be one more place to get Windows wrong,
// with nothing gained.

// Acceptance criterion 8: the second call is a no-op down to the byte.
func TestEnsurePiDefaultModelIsIdempotent(t *testing.T) {
	dir := piSettingsSandbox(t)
	path := filepath.Join(dir, "settings.json")

	if err := ensurePiDefaultModel(); err != nil {
		t.Fatalf("first ensurePiDefaultModel() error = %v", err)
	}
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := ensurePiDefaultModel(); err != nil {
		t.Fatalf("second ensurePiDefaultModel() error = %v", err)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !bytes.Equal(first, second) {
		t.Errorf("second call changed the file\nfirst:  %s\nsecond: %s", first, second)
	}
	if !strings.Contains(string(second), wantPiDefaultModel) {
		t.Errorf("settings lost the default model: %s", second)
	}
}

// vc has a second writer of the very same settings.json: the web-search
// reconciler owns the "packages" key (cmd/vc/pi_web_search.go,
// reconcileManagedPackageSetting). Both do a full read-modify-write from disk,
// so neither may drop what the other put there. This runs the pair in both
// orders and pins that the end state is identical either way — if the two ever
// grow an ordering dependency, that is the assertion that breaks.
func TestEnsurePiDefaultModelComposesWithWebSearchPackageWriter(t *testing.T) {
	const body = `{
  "theme": "nord",
  "maxTokens": 1000000,
  "editor": {"vimMode": true, "tabWidth": 2, "rulers": [80, 120]}
}`
	const pkgPath = "/managed/pi-web-access-0.13.0-void.1"

	// existing: the upgrade path, a settings.json the user already has.
	// missing: the fresh install the seed exists for — whichever writer runs
	// first creates the file, and the other must find and extend it.
	run := func(t *testing.T, seed string, first, second string) []byte {
		t.Helper()
		dir := piSettingsSandbox(t)
		path := filepath.Join(dir, "settings.json")
		if seed != "" {
			path = writePiSettings(t, dir, seed, 0600)
		}
		for _, step := range []string{first, second} {
			switch step {
			case "seed":
				if err := ensurePiDefaultModel(); err != nil {
					t.Fatalf("ensurePiDefaultModel() error = %v", err)
				}
			case "packages":
				if err := reconcileManagedPackageSetting(pkgPath, true); err != nil {
					t.Fatalf("reconcileManagedPackageSetting() error = %v", err)
				}
			}
		}
		got := readPiSettings(t, path)
		assertDefaultsWritten(t, got)
		packages, ok := got["packages"].([]any)
		if !ok || len(packages) != 1 || packages[0] != pkgPath {
			t.Errorf("packages = %#v, want [%q] (the other writer's key must survive)", got["packages"], pkgPath)
		}
		if seed != "" {
			if got["theme"] != "nord" {
				t.Errorf("theme = %#v, want %q", got["theme"], "nord")
			}
			if editor, ok := got["editor"].(map[string]any); !ok || editor["tabWidth"] != json.Number("2") {
				t.Errorf("editor = %#v, want the nested object intact", got["editor"])
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}

	for _, tc := range []struct {
		name string
		seed string
	}{
		{name: "existing settings", seed: body},
		{name: "fresh install", seed: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var seedFirst, packagesFirst []byte
			t.Run("seed then packages", func(t *testing.T) {
				seedFirst = run(t, tc.seed, "seed", "packages")
			})
			t.Run("packages then seed", func(t *testing.T) {
				packagesFirst = run(t, tc.seed, "packages", "seed")
			})
			if !bytes.Equal(seedFirst, packagesFirst) {
				t.Errorf("call order changed the result\nseed first:     %s\npackages first: %s", seedFirst, packagesFirst)
			}
		})
	}
}

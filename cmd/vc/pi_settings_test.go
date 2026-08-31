package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
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

// Acceptance criterion 4: only defaultProvider set → only defaultModel is added,
// the user's provider stays.
func TestEnsurePiDefaultModelAddsOnlyModelWhenProviderSet(t *testing.T) {
	dir := piSettingsSandbox(t)
	path := writePiSettings(t, dir, `{"defaultProvider":"void-deepseek","theme":"nord"}`, 0600)

	if err := ensurePiDefaultModel(); err != nil {
		t.Fatalf("ensurePiDefaultModel() error = %v", err)
	}

	got := readPiSettings(t, path)
	if got["defaultProvider"] != "void-deepseek" {
		t.Errorf("defaultProvider = %#v, want %q (user's choice must survive)", got["defaultProvider"], "void-deepseek")
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

// Acceptance criterion 7, first half: a file vc creates is 0600, and the atomic
// staging file does not survive the call.
func TestEnsurePiDefaultModelCreatesFileMode0600(t *testing.T) {
	dir := piSettingsSandbox(t)

	if err := ensurePiDefaultModel(); err != nil {
		t.Fatalf("ensurePiDefaultModel() error = %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("settings.json mode = %04o, want 0600", got)
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

// Acceptance criterion 7, second half: an existing file keeps its own mode —
// the write is an update, not a re-creation.
func TestEnsurePiDefaultModelKeepsExistingFileMode(t *testing.T) {
	dir := piSettingsSandbox(t)
	path := writePiSettings(t, dir, `{"theme":"nord"}`, 0644)

	if err := ensurePiDefaultModel(); err != nil {
		t.Fatalf("ensurePiDefaultModel() error = %v", err)
	}

	assertDefaultsWritten(t, readPiSettings(t, path))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0644 {
		t.Errorf("settings.json mode = %04o, want 0644 unchanged", got)
	}
}

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

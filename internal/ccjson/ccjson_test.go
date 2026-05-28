package ccjson_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/makscee/void-code/internal/ccjson"
)

// TestEnsureDefaults_Absent verifies that EnsureDefaults writes the full seed
// when ~/.claude.json does not exist.
func TestEnsureDefaults_Absent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")

	if err := ccjson.EnsureDefaults(target); err != nil {
		t.Fatalf("EnsureDefaults on absent file returned error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("file was not created: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("written content is not valid JSON: %v\ncontent: %s", err, data)
	}

	if v, ok := got["hasCompletedOnboarding"]; !ok || v != true {
		t.Errorf("hasCompletedOnboarding: want true, got %v", v)
	}
	if v, ok := got["theme"]; !ok || v != "dark" {
		t.Errorf("theme: want \"dark\", got %v", v)
	}

	// Verify mode is 0600.
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("file perm: want 0600, got %04o", perm)
	}
}

// TestEnsureDefaults_PartialFalse verifies that a file with
// hasCompletedOnboarding:false gets it flipped to true, and theme is added
// when missing, while all other keys are preserved.
func TestEnsureDefaults_PartialFalse(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")

	initial := `{"hasCompletedOnboarding":false,"apiKey":"keep-me"}`
	if err := os.WriteFile(target, []byte(initial), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := ccjson.EnsureDefaults(target); err != nil {
		t.Fatalf("EnsureDefaults returned error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after EnsureDefaults: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v\ncontent: %s", err, data)
	}

	// hasCompletedOnboarding must now be true.
	if v, ok := got["hasCompletedOnboarding"]; !ok || v != true {
		t.Errorf("hasCompletedOnboarding: want true, got %v", v)
	}
	// theme must have been added as "dark".
	if v, ok := got["theme"]; !ok || v != "dark" {
		t.Errorf("theme: want \"dark\" (added), got %v", v)
	}
	// Other keys must be preserved.
	if v, ok := got["apiKey"]; !ok || v != "keep-me" {
		t.Errorf("apiKey: want \"keep-me\" (preserved), got %v", v)
	}
}

// TestEnsureDefaults_MissingTheme verifies that a file missing the theme key
// gets theme:"dark" added while hasCompletedOnboarding and other keys are
// preserved.
func TestEnsureDefaults_MissingTheme(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")

	initial := `{"hasCompletedOnboarding":true,"extra":42}`
	if err := os.WriteFile(target, []byte(initial), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := ccjson.EnsureDefaults(target); err != nil {
		t.Fatalf("EnsureDefaults returned error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after EnsureDefaults: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v\ncontent: %s", err, data)
	}

	if v, ok := got["hasCompletedOnboarding"]; !ok || v != true {
		t.Errorf("hasCompletedOnboarding: want true, got %v", v)
	}
	if v, ok := got["theme"]; !ok || v != "dark" {
		t.Errorf("theme: want \"dark\" (added), got %v", v)
	}
	// numeric value preserved as float64 via JSON round-trip.
	if v, ok := got["extra"]; !ok || v != float64(42) {
		t.Errorf("extra: want 42 (preserved), got %v", v)
	}
}

// TestEnsureDefaults_FullyPresent verifies that a file that already has both
// keys correctly set is NOT rewritten (mtime unchanged).
func TestEnsureDefaults_FullyPresent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")

	// theme:"light" should be preserved, hasCompletedOnboarding already true.
	initial := `{"hasCompletedOnboarding":true,"theme":"light","fooBar":99}`
	if err := os.WriteFile(target, []byte(initial), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	info0, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat before: %v", err)
	}

	if err := ccjson.EnsureDefaults(target); err != nil {
		t.Fatalf("EnsureDefaults returned error: %v", err)
	}

	info1, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat after: %v", err)
	}

	// No change needed → file must not be rewritten.
	if !info1.ModTime().Equal(info0.ModTime()) {
		t.Errorf("file mtime changed — EnsureDefaults must not rewrite an already-correct file")
	}

	// Content still verbatim.
	data, _ := os.ReadFile(target)
	if string(data) != initial {
		t.Errorf("content mutated:\nwant: %s\n got: %s", initial, data)
	}
}

// TestEnsureDefaults_ExistingThemePreserved verifies that a non-dark theme
// already set in the file is never overwritten.
func TestEnsureDefaults_ExistingThemePreserved(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")

	// User chose "light", onboarding false — we must flip onboarding but keep theme.
	initial := `{"hasCompletedOnboarding":false,"theme":"light"}`
	if err := os.WriteFile(target, []byte(initial), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	if err := ccjson.EnsureDefaults(target); err != nil {
		t.Fatalf("EnsureDefaults returned error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after EnsureDefaults: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("result is not valid JSON: %v\ncontent: %s", err, data)
	}

	if v, ok := got["hasCompletedOnboarding"]; !ok || v != true {
		t.Errorf("hasCompletedOnboarding: want true, got %v", v)
	}
	// User's "light" theme MUST be preserved.
	if v, ok := got["theme"]; !ok || v != "light" {
		t.Errorf("theme: want \"light\" (user choice preserved), got %v", v)
	}
}

// TestEnsureDefaults_Unparseable verifies that a file containing invalid JSON
// is left completely untouched and an error is returned.
func TestEnsureDefaults_Unparseable(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")

	garbled := `not json at all {{{`
	if err := os.WriteFile(target, []byte(garbled), 0600); err != nil {
		t.Fatalf("setup: %v", err)
	}

	info0, _ := os.Stat(target)

	err := ccjson.EnsureDefaults(target)
	if err == nil {
		t.Fatal("EnsureDefaults must return an error for unparseable JSON")
	}

	// File must be untouched.
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatalf("read after EnsureDefaults: %v", readErr)
	}
	if string(data) != garbled {
		t.Errorf("file content changed — must not clobber unparseable JSON:\nwant: %s\n got: %s", garbled, data)
	}

	info1, _ := os.Stat(target)
	if !info1.ModTime().Equal(info0.ModTime()) {
		t.Errorf("file mtime changed — must not touch unparseable file")
	}
}

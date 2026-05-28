package ccjson_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/makscee/void-code/internal/ccjson"
)

// TestEnsureDefaults_FreshState verifies that EnsureDefaults writes the
// minimal seed JSON when ~/.claude.json does not exist.
func TestEnsureDefaults_FreshState(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")

	if err := ccjson.EnsureDefaults(target); err != nil {
		t.Fatalf("EnsureDefaults on fresh state returned error: %v", err)
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
}

// TestEnsureDefaults_PreExisting verifies that EnsureDefaults does NOT touch
// a file that already exists — content and mtime must be unchanged.
func TestEnsureDefaults_PreExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, ".claude.json")

	original := `{"hasCompletedOnboarding":false,"theme":"light","apiKey":"keep-me"}`
	if err := os.WriteFile(target, []byte(original), 0600); err != nil {
		t.Fatalf("setup: write pre-existing file: %v", err)
	}

	info0, _ := os.Stat(target)

	if err := ccjson.EnsureDefaults(target); err != nil {
		t.Fatalf("EnsureDefaults on pre-existing file returned error: %v", err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after EnsureDefaults: %v", err)
	}
	if string(data) != original {
		t.Errorf("file content mutated:\nwant: %s\n got: %s", original, data)
	}

	info1, _ := os.Stat(target)
	if !info1.ModTime().Equal(info0.ModTime()) {
		t.Errorf("file mtime changed — EnsureDefaults must not touch a pre-existing file")
	}
}

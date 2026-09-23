package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsurePiProjectModelMigrationIsScopedAndDoesNotSeed(t *testing.T) {
	_ = piSettingsSandbox(t)
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"retired owned pair", `{"defaultProvider":"void-codex","defaultModel":"gpt-5.6-luna","theme":"nord"}`, `{"defaultProvider":"void-codex","defaultModel":"gpt-6-luna","theme":"nord"}`},
		{"foreign pair", `{"defaultProvider":"anthropic","defaultModel":"gpt-5.6-luna","theme":"nord"}`, `{"defaultProvider":"anthropic","defaultModel":"gpt-5.6-luna","theme":"nord"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, ".pi", "settings.json")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			if err := ensurePiProjectModelMigration(root); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var gotJSON, wantJSON any
			if json.Unmarshal(got, &gotJSON) != nil || json.Unmarshal([]byte(tc.want), &wantJSON) != nil {
				t.Fatal("invalid test JSON")
			}
			if string(mustMarshalJSON(t, gotJSON)) != string(mustMarshalJSON(t, wantJSON)) {
				t.Fatalf("settings = %s, want %s", got, tc.want)
			}
		})
	}

	root := t.TempDir()
	if err := ensurePiProjectModelMigration(root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, ".pi", "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("missing project settings were seeded: %v", err)
	}
}

func TestReconcilePiRetiredDefaultsCanonicalProjectSuppressesGlobalOverride(t *testing.T) {
	dir := piSettingsSandbox(t)
	globalPath := writePiSettings(t, dir, `{"defaultProvider":"void-codex","defaultModel":"gpt-5.6-sol","theme":"global"}`, 0600)
	project := t.TempDir()
	projectPath := filepath.Join(project, ".pi", "settings.json")
	if err := os.MkdirAll(filepath.Dir(projectPath), 0700); err != nil {
		t.Fatal(err)
	}
	const projectBody = `{"defaultProvider":"void-codex","defaultModel":"gpt-6-luna","theme":"project"}`
	if err := os.WriteFile(projectPath, []byte(projectBody), 0600); err != nil {
		t.Fatal(err)
	}

	result := reconcilePiRetiredDefaults(project, nil)
	if result.StartupSelection != nil || len(result.Warnings) != 0 {
		t.Fatalf("reconciliation = %#v, want canonical project precedence without override", result)
	}
	if got := readPiSettings(t, globalPath); got["defaultModel"] != "gpt-6-sol" || got["theme"] != "global" {
		t.Fatalf("global settings = %#v", got)
	}
	data, err := os.ReadFile(projectPath)
	if err != nil || string(data) != projectBody {
		t.Fatalf("canonical project settings changed: data=%q err=%v", data, err)
	}
}

func TestRestorePiModelDefaultsPreservesForeignAndNormalizesRetiredSnapshots(t *testing.T) {
	for _, tc := range []struct {
		name         string
		before       map[string]any
		wantProvider string
		wantModel    string
	}{
		{"foreign", map[string]any{"defaultProvider": "anthropic", "defaultModel": "claude-current"}, "anthropic", "claude-current"},
		{"retired managed", map[string]any{"defaultProvider": "void-codex", "defaultModel": "gpt-5.6-luna"}, "void-codex", "gpt-6-luna"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := piSettingsSandbox(t)
			path := writePiSettings(t, dir, `{"defaultProvider":"void-codex","defaultModel":"gpt-6-luna","theme":"nord"}`, 0600)
			snapshot, err := snapshotPiModelDefaults(tc.before)
			if err != nil {
				t.Fatal(err)
			}
			if err := restorePiModelDefaults(snapshot, "gpt-6-luna"); err != nil {
				t.Fatal(err)
			}
			got := readPiSettings(t, path)
			if got["defaultProvider"] != tc.wantProvider || got["defaultModel"] != tc.wantModel || got["theme"] != "nord" {
				t.Fatalf("restored settings = %#v", got)
			}
		})
	}
}

func TestRestorePiModelDefaultsDoesNotOverwriteConcurrentSelection(t *testing.T) {
	dir := piSettingsSandbox(t)
	path := writePiSettings(t, dir, `{"defaultProvider":"google","defaultModel":"gemini-current","theme":"nord"}`, 0600)
	snapshot, err := snapshotPiModelDefaults(map[string]any{"defaultProvider": "anthropic", "defaultModel": "claude-current"})
	if err != nil {
		t.Fatal(err)
	}
	if err := restorePiModelDefaults(snapshot, "gpt-6-luna"); err != nil {
		t.Fatal(err)
	}
	got := readPiSettings(t, path)
	if got["defaultProvider"] != "google" || got["defaultModel"] != "gemini-current" {
		t.Fatalf("concurrent selection overwritten: %#v", got)
	}
}

func mustMarshalJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

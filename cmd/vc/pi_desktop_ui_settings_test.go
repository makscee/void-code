package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// A user opening only the desktop app must get the compact layout on their first launch.
func TestEnsurePiDesktopUIDefaultsSeedsFreshSettings(t *testing.T) {
	dir := piSettingsSandbox(t)

	if err := ensurePiDesktopUIDefaults(); err != nil {
		t.Fatalf("ensurePiDesktopUIDefaults() error = %v", err)
	}

	got := readPiSettings(t, filepath.Join(dir, "settings.json"))
	want := map[string]any{"tuiMode": "fullscreen", "hideThinkingBlock": false}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("desktop UI defaults = %#v, want %#v", got, want)
	}
}

// Defaults fill only gaps: explicit layout and reasoning choices belong to the user.
func TestEnsurePiDesktopUIDefaultsPreservesEveryExplicitChoice(t *testing.T) {
	for _, tc := range []struct {
		name         string
		body         string
		wantMode     string
		wantThinking bool
	}{
		{
			name:         "custom layout survives while thinking default is added",
			body:         `{"tuiMode":"inline","theme":"nord","lastSessionSeq":9007199254740993}`,
			wantMode:     "inline",
			wantThinking: false,
		},
		{
			name:         "hidden reasoning survives while layout default is added",
			body:         `{"hideThinkingBlock":true,"theme":"nord","lastSessionSeq":9007199254740993}`,
			wantMode:     "fullscreen",
			wantThinking: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := piSettingsSandbox(t)
			path := writePiSettings(t, dir, tc.body, 0644)
			beforeMode := statPerm(t, path)

			if err := ensurePiDesktopUIDefaults(); err != nil {
				t.Fatalf("ensurePiDesktopUIDefaults() error = %v", err)
			}

			got := readPiSettings(t, path)
			if got["tuiMode"] != tc.wantMode {
				t.Errorf("tuiMode = %#v, want %q", got["tuiMode"], tc.wantMode)
			}
			if got["hideThinkingBlock"] != tc.wantThinking {
				t.Errorf("hideThinkingBlock = %#v, want %v", got["hideThinkingBlock"], tc.wantThinking)
			}
			if got["theme"] != "nord" {
				t.Errorf("theme was not preserved: %#v", got["theme"])
			}
			if data, err := os.ReadFile(path); err != nil {
				t.Fatal(err)
			} else if !bytes.Contains(data, []byte("9007199254740993")) {
				t.Errorf("unknown integer lost precision: %s", data)
			}
			if gotMode := statPerm(t, path); gotMode != beforeMode {
				t.Errorf("settings mode = %04o, want %04o unchanged", gotMode, beforeMode)
			}
		})
	}
}

// When both choices already exist, desktop startup must leave the file byte-identical.
func TestEnsurePiDesktopUIDefaultsIsANoopForConfiguredUser(t *testing.T) {
	dir := piSettingsSandbox(t)
	const body = "{\n  \"tuiMode\": \"inline\",\n  \"hideThinkingBlock\": true,\n  \"theme\": \"nord\"\n}\n"
	path := writePiSettings(t, dir, body, 0600)

	if err := ensurePiDesktopUIDefaults(); err != nil {
		t.Fatalf("ensurePiDesktopUIDefaults() error = %v", err)
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != body {
		t.Fatalf("configured settings were rewritten:\n%s", after)
	}
}

// Malformed user configuration must be reported without destructive recovery.
func TestEnsurePiDesktopUIDefaultsRefusesMalformedSettings(t *testing.T) {
	dir := piSettingsSandbox(t)
	const body = `{"tuiMode":`
	path := writePiSettings(t, dir, body, 0600)

	err := ensurePiDesktopUIDefaults()
	if err == nil {
		t.Fatal("malformed settings accepted")
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(after) != body {
		t.Fatalf("malformed settings were modified: %q", after)
	}
}

// All settings owners must converge on one result regardless of desktop startup ordering.
func TestPiDesktopUIDefaultsCommuteWithExistingSettingsWriters(t *testing.T) {
	orders := [][]string{
		{"model", "packages", "ui"},
		{"model", "ui", "packages"},
		{"packages", "model", "ui"},
		{"packages", "ui", "model"},
		{"ui", "model", "packages"},
		{"ui", "packages", "model"},
	}
	var reference []byte
	for _, order := range orders {
		name := order[0] + "-" + order[1] + "-" + order[2]
		t.Run(name, func(t *testing.T) {
			dir := piSettingsSandbox(t)
			path := writePiSettings(t, dir, `{"theme":"nord","lastSessionSeq":9007199254740993}`, 0600)
			for _, step := range order {
				var err error
				switch step {
				case "model":
					err = ensurePiDefaultModel()
				case "packages":
					err = reconcileManagedPackageSetting(contractPackagePath, true)
				case "ui":
					err = ensurePiDesktopUIDefaults()
				}
				if err != nil {
					t.Fatalf("%s writer failed: %v", step, err)
				}
			}
			got := readPiSettings(t, path)
			if got["theme"] != "nord" || got["defaultProvider"] != wantPiDefaultProvider || got["defaultModel"] != wantPiDefaultModel || got["tuiMode"] != "fullscreen" || got["hideThinkingBlock"] != false {
				t.Fatalf("incomplete converged settings: %#v", got)
			}
			packages, ok := got["packages"].([]any)
			if !ok || len(packages) != 1 || packages[0] != contractPackagePath {
				t.Fatalf("packages = %#v, want [%q]", got["packages"], contractPackagePath)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if reference == nil {
				reference = data
			} else if !bytes.Equal(data, reference) {
				t.Fatalf("writer order changed settings\nreference: %s\ncurrent:   %s", reference, data)
			}
		})
	}
}

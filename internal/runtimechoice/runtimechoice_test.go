package runtimechoice

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/makscee/void-code/internal/config"
)

// withHome points the vc config file (~/.void-code/config) at a throwaway home.
func withHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	return home
}

func writeRawConfig(t *testing.T, body string) string {
	t.Helper()
	path, err := config.ConfigFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestPersistedFormIsTheSpecValue(t *testing.T) {
	if Pi != Runtime("pi") {
		t.Fatalf("Pi = %q, want %q", Pi, "pi")
	}
	if Codex != Runtime("codex") {
		t.Fatalf("Codex = %q, want %q", Codex, "codex")
	}
}

func TestParseAcceptsOnlyPiAndCodex(t *testing.T) {
	for in, want := range map[string]Runtime{
		"pi":       Pi,
		"codex":    Codex,
		"  pi  ":   Pi,
		" codex\n": Codex,
	} {
		got, err := Parse(in)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v", in, err)
		}
		if got != want {
			t.Fatalf("Parse(%q) = %q, want %q", in, got, want)
		}
	}
	// Legacy harness names and garbage are refused, not degraded to a default:
	// `vc runtime claude` must be an error the person sees.
	for _, in := range []string{"", "   ", "claude", "foo", "pi codex", "piX", "codex2"} {
		if got, err := Parse(in); err == nil {
			t.Fatalf("Parse(%q) = %q, nil; want an error", in, got)
		}
	}
}

func TestLoadReportsAbsentWhenNothingWasChosen(t *testing.T) {
	withHome(t)
	got, saved, err := Load()
	if err != nil {
		t.Fatalf("Load() with no config file: %v", err)
	}
	if saved {
		t.Fatalf("Load() with no config file reported a saved choice %q", got)
	}
	if got == Codex {
		t.Fatal("Load() with no config file returned Codex")
	}
}

// People have carried active_harness=codex (or claude) since June. That key
// predates the runtime choice and must never pick the runtime for them: the
// spec keeps legacy keys ignored so nobody silently gets a different runtime.
func TestLoadIgnoresLegacyActiveHarness(t *testing.T) {
	for _, legacy := range []string{"codex", "claude", "pi"} {
		t.Run(legacy, func(t *testing.T) {
			withHome(t)
			writeRawConfig(t, "active_harness="+legacy+"\nactive_provider=chatgpt-new\nauto_update=true\n")
			got, saved, err := Load()
			if err != nil {
				t.Fatalf("Load(): %v", err)
			}
			if saved {
				t.Fatalf("legacy active_harness=%s was read as a saved runtime %q", legacy, got)
			}
			if got == Codex {
				t.Fatalf("legacy active_harness=%s made the runtime Codex", legacy)
			}
		})
	}
}

func TestLoadReadsTheRuntimeKey(t *testing.T) {
	for _, tc := range []struct {
		body string
		want Runtime
	}{
		{"runtime=codex\n", Codex},
		{"runtime=pi\n", Pi},
		// A saved choice wins over the legacy key, whatever the legacy key says.
		{"active_harness=codex\nruntime=pi\n", Pi},
		{"active_harness=pi\nruntime=codex\n", Codex},
	} {
		withHome(t)
		writeRawConfig(t, tc.body)
		got, saved, err := Load()
		if err != nil {
			t.Fatalf("Load() for %q: %v", tc.body, err)
		}
		if !saved || got != tc.want {
			t.Fatalf("Load() for %q = (%q, %v), want (%q, true)", tc.body, got, saved, tc.want)
		}
	}
}

// A hand-edited unknown value is not a choice; Load says so instead of quietly
// turning it into one of the runtimes.
func TestLoadRefusesAnUnknownSavedValue(t *testing.T) {
	withHome(t)
	writeRawConfig(t, "runtime=claude\n")
	got, _, err := Load()
	if err == nil {
		t.Fatalf("Load() with runtime=claude = %q, nil; want an error", got)
	}
}

func TestSaveWritesRuntimeAndKeepsEveryOtherKey(t *testing.T) {
	withHome(t)
	writeRawConfig(t, "auto_update=true\nlast_prompted_version=v0.2.59\nactive_harness=claude\nstatusline_skipped=true\n")

	if err := Save(Codex); err != nil {
		t.Fatalf("Save(Codex): %v", err)
	}
	kv, err := config.ReadConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"runtime":               "codex",
		"auto_update":           "true",
		"last_prompted_version": "v0.2.59",
		"active_harness":        "claude",
		"statusline_skipped":    "true",
	}
	for k, v := range want {
		if kv[k] != v {
			t.Errorf("after Save(Codex) %s = %q, want %q (config: %v)", k, kv[k], v, kv)
		}
	}
	if prefs := config.ReadUpdatePrefs(); !prefs.AutoUpdate {
		t.Error("Save dropped the auto-update preference")
	}

	if err := Save(Pi); err != nil {
		t.Fatalf("Save(Pi): %v", err)
	}
	got, saved, err := Load()
	if err != nil || !saved || got != Pi {
		t.Fatalf("Load() after Save(Pi) = (%q, %v, %v), want (pi, true, nil)", got, saved, err)
	}
}

func TestSaveRefusesAnUnknownRuntimeAndLeavesTheFileAlone(t *testing.T) {
	withHome(t)
	path := writeRawConfig(t, "auto_update=true\n")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Runtime{"", "claude", "foo"} {
		if err := Save(bad); err == nil {
			t.Fatalf("Save(%q) = nil; want an error", bad)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("refused Save changed the config file:\nbefore: %q\nafter:  %q", before, after)
	}
}
